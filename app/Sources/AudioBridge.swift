import Foundation
import CoreAudio
import AudioToolbox
import AVFoundation

struct CallAudioDevice: Identifiable, Equatable {
    let uid: String
    let name: String
    var id: String { uid }
}

struct CallAudioState: Equatable {
    enum Phase: Equatable { case idle, connecting, running, waitingForDevice, failed }

    var inputs: [CallAudioDevice] = []
    var outputs: [CallAudioDevice] = []
    // 空 UID 表示跟随系统；AudioDeviceID 只在本次枚举内使用。
    var inputUID = ""
    var outputUID = ""
    var inputName = ""
    var outputName = ""
    var activeInputName: String?
    var activeOutputName: String?
    var phase: Phase = .idle
    var message: String?
    var isRunning: Bool { phase == .running }
}

private struct DeviceFormat: Equatable {
    var sampleRate: Double
    var channels: UInt32
    var bits: UInt32 = 16
    var isFloat = false
    var isNonInterleaved = false
    var isSupportedPCM = true
}

/// 生命周期和设备通知只在 controlQueue 上处理；实时回调只接触本次管线的样本。
/// 下行：模块 UAC → VoiceProcessingIO 播放（回声参考）。
/// 上行：同一 VoiceProcessingIO 的 AEC/AGC 麦克风 → 模块 UAC。
// 可变状态全部由 controlQueue 隔离，发送到主线程的状态是值类型快照。
final class AudioBridge: @unchecked Sendable {
    static let shared = AudioBridge()

    private struct Device: Equatable {
        let id: AudioDeviceID
        let uid: String
        let name: String
        let input: DeviceFormat?
        let output: DeviceFormat?
        let isModule: Bool
        var option: CallAudioDevice { CallAudioDevice(uid: uid, name: name) }
    }

    private struct Route: Equatable {
        let moduleInput: Device
        let moduleOutput: Device
        let microphone: Device
        let speaker: Device
    }

    private struct Listener {
        let object: AudioObjectID
        var address: AudioObjectPropertyAddress
        let block: AudioObjectPropertyListenerBlock
    }

    private struct IORegistration {
        let device: AudioDeviceID
        let procID: AudioDeviceIOProcID
        let context: UnsafeMutableRawPointer
    }

    private struct BridgeError: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    private let controlQueue = DispatchQueue(label: "com.djonehub.call-audio", qos: .userInitiated)
    private let defaults = UserDefaults.standard
    private var state = CallAudioState()
    private var observer: ((CallAudioState) -> Void)?
    private var devices: [Device] = []
    private var listeners: [Listener] = []
    private var watchedDevices = Set<AudioDeviceID>()
    private var wantsAudio = false
    private var engine: AVAudioEngine?
    private var engineObserver: NSObjectProtocol?
    private var registrations: [IORegistration] = []
    private var activeRoute: Route?
    private var attemptedRoute: Route?
    private var healthTimer: DispatchSourceTimer?
    private var reconfigureWork: DispatchWorkItem?

    private init() {
        state.inputUID = defaults.string(forKey: "callAudioInputUID") ?? ""
        state.outputUID = defaults.string(forKey: "callAudioOutputUID") ?? ""
        state.inputName = defaults.string(forKey: "callAudioInputName") ?? ""
        state.outputName = defaults.string(forKey: "callAudioOutputName") ?? ""
    }

    func observe(_ observer: @escaping (CallAudioState) -> Void) {
        controlQueue.async {
            self.observer = observer
            if self.listeners.isEmpty {
                for selector in [kAudioHardwarePropertyDevices, kAudioHardwarePropertyDefaultInputDevice,
                                 kAudioHardwarePropertyDefaultOutputDevice] {
                    self.addListener(object: AudioObjectID(kAudioObjectSystemObject), selector: selector)
                }
            }
            self.refreshDevices()
            self.publish()
        }
    }

    func selectInput(_ uid: String) {
        controlQueue.async {
            self.state.inputUID = uid
            self.state.inputName = self.devices.first { $0.uid == uid }?.name ?? ""
            self.defaults.set(uid, forKey: "callAudioInputUID")
            self.defaults.set(self.state.inputName, forKey: "callAudioInputName")
            self.selectionChanged()
        }
    }

    func selectOutput(_ uid: String) {
        controlQueue.async {
            self.state.outputUID = uid
            self.state.outputName = self.devices.first { $0.uid == uid }?.name ?? ""
            self.defaults.set(uid, forKey: "callAudioOutputUID")
            self.defaults.set(self.state.outputName, forKey: "callAudioOutputName")
            self.selectionChanged()
        }
    }

    private func selectionChanged() {
        if wantsAudio {
            // 切换只重建 Mac 管线，不重复改变模块路由，也不挂断蜂窝电话。
            scheduleReconfigure()
        } else {
            publish()
        }
    }

    func requestMicrophoneAccess() async -> Bool {
        switch AVCaptureDevice.authorizationStatus(for: .audio) {
        case .authorized: return true
        case .notDetermined:
            return await withCheckedContinuation { continuation in
                AVCaptureDevice.requestAccess(for: .audio) { continuation.resume(returning: $0) }
            }
        case .denied, .restricted: return false
        @unknown default: return false
        }
    }

    func start() {
        controlQueue.async {
            self.wantsAudio = true
            self.reconfigure()
        }
    }

    func retry() {
        controlQueue.async {
            guard self.wantsAudio else { return }
            self.reconfigure()
        }
    }

    func stop() async {
        await withCheckedContinuation { continuation in
            controlQueue.async {
                self.wantsAudio = false
                self.reconfigureWork?.cancel()
                self.reconfigureWork = nil
                self.stopPipeline()
                self.attemptedRoute = nil
                self.state.phase = .idle
                self.state.message = nil
                self.publish()
                // 调用者必须等主机 IO 释放后再发送 ATH/模块路由停止请求。
                continuation.resume()
            }
        }
    }

    private func publish() {
        let snapshot = state
        let callback = observer
        DispatchQueue.main.async { callback?(snapshot) }
    }

    // MARK: - 设备枚举和监听

    private func stringProperty(_ id: AudioObjectID, _ selector: AudioObjectPropertySelector) -> String {
        var address = propertyAddress(selector)
        var value: Unmanaged<CFString>?
        var size = UInt32(MemoryLayout<Unmanaged<CFString>?>.size)
        guard AudioObjectGetPropertyData(id, &address, 0, nil, &size, &value) == noErr,
              let value else { return "" }
        return value.takeRetainedValue() as String
    }

    private func uintProperty(_ id: AudioObjectID, _ selector: AudioObjectPropertySelector) -> UInt32? {
        var address = propertyAddress(selector)
        var value: UInt32 = 0
        var size = UInt32(MemoryLayout<UInt32>.size)
        guard AudioObjectGetPropertyData(id, &address, 0, nil, &size, &value) == noErr else { return nil }
        return value
    }

    private func propertyAddress(_ selector: AudioObjectPropertySelector,
                                 scope: AudioObjectPropertyScope = kAudioObjectPropertyScopeGlobal) -> AudioObjectPropertyAddress {
        AudioObjectPropertyAddress(mSelector: selector, mScope: scope, mElement: kAudioObjectPropertyElementMain)
    }

    private func streamFormat(device: AudioDeviceID, scope: AudioObjectPropertyScope) -> DeviceFormat? {
        guard hasChannels(device: device, scope: scope) else { return nil }
        var address = propertyAddress(kAudioDevicePropertyStreamFormat, scope: scope)
        var format = AudioStreamBasicDescription()
        var size = UInt32(MemoryLayout<AudioStreamBasicDescription>.size)
        guard AudioObjectGetPropertyData(device, &address, 0, nil, &size, &format) == noErr else { return nil }
        let isFloat = format.mFormatFlags & kLinearPCMFormatFlagIsFloat != 0
        let isSignedInteger = format.mFormatFlags & kLinearPCMFormatFlagIsSignedInteger != 0
        let isBigEndian = format.mFormatFlags & kLinearPCMFormatFlagIsBigEndian != 0
        let supportedWidth = isFloat ? [32, 64].contains(Int(format.mBitsPerChannel))
                                     : [16, 32].contains(Int(format.mBitsPerChannel))
        guard format.mSampleRate.isFinite, format.mSampleRate > 0, format.mChannelsPerFrame > 0 else { return nil }
        let isSupportedPCM = format.mFormatID == kAudioFormatLinearPCM && supportedWidth
            && (isFloat || isSignedInteger) && !isBigEndian
        return DeviceFormat(sampleRate: format.mSampleRate, channels: format.mChannelsPerFrame,
                            bits: format.mBitsPerChannel, isFloat: isFloat,
                            isNonInterleaved: format.mFormatFlags & kAudioFormatFlagIsNonInterleaved != 0,
                            isSupportedPCM: isSupportedPCM)
    }

    private func hasChannels(device: AudioDeviceID, scope: AudioObjectPropertyScope) -> Bool {
        var address = propertyAddress(kAudioDevicePropertyStreamConfiguration, scope: scope)
        var size: UInt32 = 0
        guard AudioObjectGetPropertyDataSize(device, &address, 0, nil, &size) == noErr,
              size >= MemoryLayout<AudioBufferList>.size else { return false }
        let memory = UnsafeMutableRawPointer.allocate(byteCount: Int(size), alignment: MemoryLayout<AudioBufferList>.alignment)
        defer { memory.deallocate() }
        guard AudioObjectGetPropertyData(device, &address, 0, nil, &size, memory) == noErr else { return false }
        let buffers = memory.assumingMemoryBound(to: AudioBufferList.self)
        return UnsafeMutableAudioBufferListPointer(buffers).contains { $0.mNumberChannels > 0 }
    }

    private func deviceIDs() -> [AudioDeviceID] {
        var address = propertyAddress(kAudioHardwarePropertyDevices)
        let system = AudioObjectID(kAudioObjectSystemObject)
        var size: UInt32 = 0
        guard AudioObjectGetPropertyDataSize(system, &address, 0, nil, &size) == noErr, size > 0 else { return [] }
        var ids = [AudioDeviceID](repeating: 0, count: Int(size) / MemoryLayout<AudioDeviceID>.size)
        guard AudioObjectGetPropertyData(system, &address, 0, nil, &size, &ids) == noErr else { return [] }
        return ids
    }

    private func refreshDevices() {
        let ids = deviceIDs()
        devices = ids.compactMap { id in
            guard uintProperty(id, kAudioDevicePropertyDeviceIsAlive) == 1,
                  uintProperty(id, kAudioDevicePropertyIsHidden) != 1 else { return nil }
            let uid = stringProperty(id, kAudioDevicePropertyDeviceUID)
            let name = stringProperty(id, kAudioObjectPropertyName)
            let identity = "\(uid) \(name)".lowercased()
            // VoiceProcessingIO 的内部聚合设备不是用户可选的麦克风/扬声器。
            guard !uid.isEmpty, !identity.contains("voiceprocessing"),
                  !identity.contains("vpau"), !identity.contains("vpio") else { return nil }
            let isModule = ["ac interface", "as interface", "baiwang", "百旺", "quectel"].contains { identity.contains($0) }
            return Device(id: id, uid: uid, name: name.isEmpty ? uid : name,
                          input: streamFormat(device: id, scope: kAudioObjectPropertyScopeInput),
                          output: streamFormat(device: id, scope: kAudioObjectPropertyScopeOutput), isModule: isModule)
        }
        devices.sort { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
        state.inputs = devices.filter { !$0.isModule && $0.input != nil }.map(\.option)
        state.outputs = devices.filter { !$0.isModule && $0.output != nil }.map(\.option)
        // 暂时 isAlive=0 的设备仍需监听，恢复时不一定会发生设备列表变更。
        updateDeviceListeners(Set(ids))
    }

    private func addListener(object: AudioObjectID, selector: AudioObjectPropertySelector,
                             scope: AudioObjectPropertyScope = kAudioObjectPropertyScopeGlobal) {
        var address = propertyAddress(selector, scope: scope)
        guard AudioObjectHasProperty(object, &address) else { return }
        let block: AudioObjectPropertyListenerBlock = { [weak self] _, _ in self?.devicesChanged() }
        guard AudioObjectAddPropertyListenerBlock(object, &address, controlQueue, block) == noErr else { return }
        listeners.append(Listener(object: object, address: address, block: block))
    }

    private func updateDeviceListeners(_ current: Set<AudioDeviceID>) {
        guard current != watchedDevices else { return }
        for var listener in listeners where listener.object != AudioObjectID(kAudioObjectSystemObject) {
            AudioObjectRemovePropertyListenerBlock(listener.object, &listener.address, controlQueue, listener.block)
        }
        listeners.removeAll { $0.object != AudioObjectID(kAudioObjectSystemObject) }
        watchedDevices = current
        for id in current {
            addListener(object: id, selector: kAudioDevicePropertyDeviceIsAlive)
            addListener(object: id, selector: kAudioDevicePropertyNominalSampleRate)
            for scope in [kAudioObjectPropertyScopeInput, kAudioObjectPropertyScopeOutput] {
                addListener(object: id, selector: kAudioDevicePropertyStreamFormat, scope: scope)
                addListener(object: id, selector: kAudioDevicePropertyStreamConfiguration, scope: scope)
            }
        }
    }

    private func devicesChanged() {
        refreshDevices()
        if wantsAudio && (try? resolveRoute()) != attemptedRoute {
            scheduleReconfigure()
        } else {
            publish()
        }
    }

    private func hostDevice(uid: String, isInput: Bool) throws -> Device {
        let label = isInput ? "麦克风" : "扬声器／耳机"
        let selectedName = isInput ? state.inputName : state.outputName
        let device: Device?
        if uid.isEmpty {
            let selector = isInput ? kAudioHardwarePropertyDefaultInputDevice : kAudioHardwarePropertyDefaultOutputDevice
            let id = uintProperty(AudioObjectID(kAudioObjectSystemObject), selector)
            device = devices.first { $0.id == id }
        } else {
            device = devices.first { $0.uid == uid }
        }
        guard let device, !device.isModule, (isInput ? device.input : device.output) != nil else {
            let name = selectedName.isEmpty ? label : selectedName
            let reason = uid.isEmpty ? "系统默认\(label)不可用或指向模块音频接口" : "所选\(label)「\(name)」已断开或不可用"
            throw BridgeError(message: "\(reason)。音频已暂停，重新连接或选择其他设备后恢复。")
        }
        return device
    }

    private func resolveRoute() throws -> Route {
        let microphone = try hostDevice(uid: state.inputUID, isInput: true)
        let speaker = try hostDevice(uid: state.outputUID, isInput: false)
        guard let moduleInput = devices.first(where: { $0.isModule && $0.input != nil }) else {
            throw BridgeError(message: "未找到模块音频输入设备（AC Interface）。音频已暂停，等待模块重新连接。")
        }
        guard let moduleOutput = devices.first(where: { $0.isModule && $0.output != nil }) else {
            throw BridgeError(message: "未找到模块音频输出设备（AS Interface）。音频已暂停，等待模块重新连接。")
        }
        return Route(moduleInput: moduleInput, moduleOutput: moduleOutput, microphone: microphone, speaker: speaker)
    }

    // MARK: - 音频管线生命周期

    private func scheduleReconfigure() {
        reconfigureWork?.cancel()
        // 立即停止旧设备，避免设备移除后继续显示运行或意外从其他输出播放。
        stopPipeline()
        state.phase = .connecting
        state.message = "正在切换音频设备…"
        publish()
        let work = DispatchWorkItem { [weak self] in self?.reconfigure() }
        reconfigureWork = work
        controlQueue.asyncAfter(deadline: .now() + .milliseconds(250), execute: work)
    }

    private func reconfigure() {
        guard wantsAudio else { return }
        reconfigureWork?.cancel()
        reconfigureWork = nil
        stopPipeline()
        refreshDevices()
        let route: Route
        do {
            route = try resolveRoute()
        } catch {
            attemptedRoute = nil
            state.phase = .waitingForDevice
            state.message = error.localizedDescription
            publish()
            return
        }
        attemptedRoute = route
        state.phase = .connecting
        state.message = "正在建立通话音频…"
        publish()
        do {
            try startPipeline(route)
            // 启用 VoiceProcessingIO 后硬件格式可能改变，以最终格式作为监听基线。
            refreshDevices()
            let resolved = try resolveRoute()
            guard resolved.microphone.id == route.microphone.id, resolved.speaker.id == route.speaker.id,
                  resolved.moduleInput == route.moduleInput, resolved.moduleOutput == route.moduleOutput else {
                // UAC 重新枚举/换格式时旧 IOProc 仍绑定旧设备；新路线必须实际重建。
                scheduleReconfigure()
                return
            }
            activeRoute = resolved
            attemptedRoute = resolved
            state.phase = .running
            state.message = nil
            state.activeInputName = route.microphone.name
            state.activeOutputName = route.speaker.name
            startHealthCheck()
        } catch {
            stopPipeline()
            // 保存停止语音处理后的设备状态，忽略本次失败造成的格式通知，避免无限重试。
            refreshDevices()
            attemptedRoute = try? resolveRoute()
            state.phase = .failed
            state.message = "通话音频未连接：\(error.localizedDescription) 请重试或选择其他输入、输出设备。"
        }
        publish()
    }

    private func setDevice(_ id: AudioDeviceID, unit: AudioUnit, bus: AudioUnitElement) throws {
        var device = id
        let status = AudioUnitSetProperty(unit, kAudioOutputUnitProperty_CurrentDevice,
                                          kAudioUnitScope_Global, bus, &device, UInt32(MemoryLayout.size(ofValue: device)))
        guard status == noErr else { throw BridgeError(message: "所选设备无法用于语音处理（\(status)）。") }
    }

    private func verifyDevice(_ expected: AudioDeviceID, unit: AudioUnit, bus: AudioUnitElement) throws {
        var actual: AudioDeviceID = 0
        var size = UInt32(MemoryLayout.size(ofValue: actual))
        let status = AudioUnitGetProperty(unit, kAudioOutputUnitProperty_CurrentDevice,
                                          kAudioUnitScope_Global, bus, &actual, &size)
        guard status == noErr, actual == expected else {
            throw BridgeError(message: "语音处理未使用所选音频设备（\(status)），已停止音频以防意外外放。")
        }
    }

    private func startPipeline(_ route: Route) throws {
        guard let moduleInputFormat = route.moduleInput.input, let moduleOutputFormat = route.moduleOutput.output,
              moduleInputFormat.isSupportedPCM, moduleOutputFormat.isSupportedPCM,
              let voiceFormat = AVAudioFormat(standardFormatWithSampleRate: 48_000, channels: 1) else {
            throw BridgeError(message: "设备 PCM 音频格式不受支持。")
        }
        let engine = AVAudioEngine()
        self.engine = engine
        // 先启用，随后重新取得底层单元：切换 VoiceProcessing 会替换 I/O AudioUnit。
        try engine.inputNode.setVoiceProcessingEnabled(true)
        guard engine.inputNode.isVoiceProcessingEnabled, engine.outputNode.isVoiceProcessingEnabled,
              let inputUnit = engine.inputNode.audioUnit, let outputUnit = engine.outputNode.audioUnit else {
            throw BridgeError(message: "无法启用 macOS 回声消除。")
        }
        // VoiceProcessingIO 的 global bus 1 是采集设备，bus 0 是播放设备。
        // 同一单元允许不同设备，不能通过全局系统默认设备实现应用内选择。
        try setDevice(route.microphone.id, unit: inputUnit, bus: 1)
        try setDevice(route.speaker.id, unit: outputUnit, bus: 0)
        engine.inputNode.isVoiceProcessingBypassed = false
        engine.inputNode.isVoiceProcessingAGCEnabled = true

        let pipeline = AudioPipeline(moduleInput: moduleInputFormat, moduleOutput: moduleOutputFormat,
                                     voice: DeviceFormat(sampleRate: voiceFormat.sampleRate, channels: 1,
                                                         bits: 32, isFloat: true, isNonInterleaved: true))
        let source = AVAudioSourceNode(format: voiceFormat) { isSilence, _, _, output in
            isSilence.pointee = ObjCBool(!pipeline.renderDownstream(output))
            return noErr
        }
        let sink = AVAudioSinkNode { _, _, input in
            pipeline.captureMicrophone(input)
            return noErr
        }
        engine.attach(source)
        engine.attach(sink)
        // VoiceProcessingIO 支持硬件采样率转换；两端客户端格式必须保持相同。
        engine.connect(engine.inputNode, to: sink, format: voiceFormat)
        engine.connect(source, to: engine.mainMixerNode, format: voiceFormat)
        engine.connect(engine.mainMixerNode, to: engine.outputNode, format: voiceFormat)
        engine.prepare()
        try engine.start()
        try verifyDevice(route.microphone.id, unit: inputUnit, bus: 1)
        try verifyDevice(route.speaker.id, unit: outputUnit, bus: 0)
        guard engine.isRunning, !engine.inputNode.isVoiceProcessingBypassed,
              engine.inputNode.isVoiceProcessingAGCEnabled else {
            throw BridgeError(message: "macOS 语音处理未正常运行。")
        }
        try registerModuleIO(route, pipeline: pipeline)
        engineObserver = NotificationCenter.default.addObserver(forName: .AVAudioEngineConfigurationChange,
                                                                object: engine, queue: nil) { [weak self, weak engine] _ in
            self?.controlQueue.async { [weak self, weak engine] in
                guard let self, let engine, self.engine === engine, self.wantsAudio else { return }
                self.refreshDevices()
                // 忽略启用语音处理本身的延迟通知，只重建已停止或设备已变化的管线。
                if !engine.isRunning || (try? self.resolveRoute()) != self.attemptedRoute {
                    self.scheduleReconfigure()
                }
            }
        }
    }

    private func registerModuleIO(_ route: Route, pipeline: AudioPipeline) throws {
        // 全双工模块只注册一次，避免同一个设备的两个 IOProc 互相覆盖输出。
        for device in Set([route.moduleInput.id, route.moduleOutput.id]) {
            let context = AudioIOContext(pipeline: pipeline, capture: device == route.moduleInput.id,
                                         playback: device == route.moduleOutput.id)
            let pointer = Unmanaged.passRetained(context).toOpaque()
            var procID: AudioDeviceIOProcID?
            let status = AudioDeviceCreateIOProcID(device, { _, _, input, _, output, _, pointer in
                guard let pointer else { return noErr }
                let context = Unmanaged<AudioIOContext>.fromOpaque(pointer).takeUnretainedValue()
                context.pipeline.moduleIO(input: input, output: output, capture: context.capture, playback: context.playback)
                return noErr
            }, pointer, &procID)
            guard status == noErr, let procID else {
                Unmanaged<AudioIOContext>.fromOpaque(pointer).release()
                throw BridgeError(message: "模块音频设备注册失败（\(status)）。")
            }
            registrations.append(IORegistration(device: device, procID: procID, context: pointer))
            let startStatus = AudioDeviceStart(device, procID)
            guard startStatus == noErr else { throw BridgeError(message: "模块音频设备启动失败（\(startStatus)）。") }
        }
    }

    private func startHealthCheck() {
        let timer = DispatchSource.makeTimerSource(queue: controlQueue)
        timer.schedule(deadline: .now() + .seconds(2), repeating: .seconds(2))
        timer.setEventHandler { [weak self] in
            guard let self, self.wantsAudio, let route = self.activeRoute else { return }
            self.refreshDevices()
            if (try? self.resolveRoute()) != route {
                // 同时检查模块，避免 UAC 失效但主机引擎仍运行时出现无声假运行。
                self.scheduleReconfigure()
                return
            }
            do {
                guard let engine = self.engine, engine.isRunning,
                      engine.inputNode.isVoiceProcessingEnabled, !engine.inputNode.isVoiceProcessingBypassed,
                      let input = engine.inputNode.audioUnit, let output = engine.outputNode.audioUnit else {
                    throw BridgeError(message: "音频引擎已停止，请重新连接音频。")
                }
                try self.verifyDevice(route.microphone.id, unit: input, bus: 1)
                try self.verifyDevice(route.speaker.id, unit: output, bus: 0)
            } catch {
                self.refreshDevices()
                if (try? self.resolveRoute()) != route {
                    // 健康检查可能早于硬件通知抵达；新路线必须真正重建，不能标为已尝试。
                    self.scheduleReconfigure()
                    return
                }
                self.stopPipeline()
                self.refreshDevices()
                self.attemptedRoute = try? self.resolveRoute()
                self.state.phase = .failed
                self.state.message = error.localizedDescription
                self.publish()
            }
        }
        healthTimer = timer
        timer.resume()
    }

    private func stopPipeline() {
        healthTimer?.cancel()
        healthTimer = nil
        if let engineObserver { NotificationCenter.default.removeObserver(engineObserver) }
        engineObserver = nil
        engine?.stop()
        engine = nil
        for registration in registrations.reversed() {
            AudioDeviceStop(registration.device, registration.procID)
            AudioDeviceDestroyIOProcID(registration.device, registration.procID)
            Unmanaged<AudioIOContext>.fromOpaque(registration.context).release()
        }
        registrations.removeAll()
        activeRoute = nil
        state.activeInputName = nil
        state.activeOutputName = nil
        // 每次重建都创建独立 AudioPipeline，不让旧通话/旧设备样本进入新管线。
    }
}

private final class AudioIOContext {
    let pipeline: AudioPipeline
    let capture: Bool
    let playback: Bool

    init(pipeline: AudioPipeline, capture: Bool, playback: Bool) {
        self.pipeline = pipeline
        self.capture = capture
        self.playback = playback
    }
}

private final class AudioPipeline {
    private let moduleInput: DeviceFormat
    private let moduleOutput: DeviceFormat
    private let voice: DeviceFormat
    private let downResampler: FloatResampler
    private let upResampler: FloatResampler
    private var downSamples: [Float32] = []
    private var upSamples: [Float32] = []
    private let lock = NSLock()

    init(moduleInput: DeviceFormat, moduleOutput: DeviceFormat, voice: DeviceFormat) {
        self.moduleInput = moduleInput
        self.moduleOutput = moduleOutput
        self.voice = voice
        downResampler = FloatResampler(inRate: moduleInput.sampleRate, outRate: voice.sampleRate)
        upResampler = FloatResampler(inRate: voice.sampleRate, outRate: moduleOutput.sampleRate)
    }

    func moduleIO(input: UnsafePointer<AudioBufferList>?, output: UnsafeMutablePointer<AudioBufferList>?,
                  capture: Bool, playback: Bool) {
        if let output { silence(output) }
        guard lock.try() else { return }
        defer { lock.unlock() }
        if capture, let input {
            downSamples.append(contentsOf: downResampler.process(readSamples(input, format: moduleInput)))
            capSamples(&downSamples, sampleRate: voice.sampleRate)
        }
        if playback, let output { writeSamples(&upSamples, format: moduleOutput, into: output) }
    }

    func captureMicrophone(_ input: UnsafePointer<AudioBufferList>) {
        guard lock.try() else { return }
        defer { lock.unlock() }
        upSamples.append(contentsOf: upResampler.process(readSamples(input, format: voice)))
        capSamples(&upSamples, sampleRate: moduleOutput.sampleRate)
    }

    func renderDownstream(_ output: UnsafeMutablePointer<AudioBufferList>) -> Bool {
        silence(output)
        guard lock.try() else { return false }
        defer { lock.unlock() }
        let hasSamples = !downSamples.isEmpty
        writeSamples(&downSamples, format: voice, into: output)
        return hasSamples
    }

    private func silence(_ output: UnsafeMutablePointer<AudioBufferList>) {
        for buffer in UnsafeMutableAudioBufferListPointer(output) {
            if let data = buffer.mData { memset(data, 0, Int(buffer.mDataByteSize)) }
        }
    }

    private func readSamples(_ input: UnsafePointer<AudioBufferList>, format: DeviceFormat) -> [Float32] {
        var data = Data()
        appendBufferList(input, to: &data)
        return takeSamples(from: &data, format: format)
    }

    private func appendBufferList(_ buffers: UnsafePointer<AudioBufferList>, to data: inout Data) {
        let pointer = UnsafeMutablePointer<AudioBufferList>(mutating: buffers)
        let list = UnsafeMutableAudioBufferListPointer(pointer)
        // 非交错多声道输入的每个 AudioBuffer 是一个独立声道；通话桥需要单声道，
        // 因此只取第一个缓冲。单缓冲的交错格式仍按 ASBD channels 正常降混。
        let selected = list.count > 1 ? list.prefix(1) : list[...]
        for buffer in selected {
            if let base = buffer.mData {
                data.append(base.assumingMemoryBound(to: UInt8.self), count: Int(buffer.mDataByteSize))
            }
        }
        let maximumBufferedBytes = 1_048_576
        if data.count > maximumBufferedBytes {
            data.removeFirst(data.count - maximumBufferedBytes)
        }
    }

    /// 按设备位深解析为 Float32，通话取第一个声道。
    private func takeSamples(from data: inout Data, format: DeviceFormat) -> [Float32] {
        // CoreAudio exposes non-interleaved channels as separate AudioBuffers;
        // appendBufferList intentionally keeps the first channel, so its byte
        // queue contains one sample per frame rather than all ASBD channels.
        let channels = format.isNonInterleaved ? 1 : max(1, Int(format.channels))
        let bytesPerSample = Int(format.bits) / 8
        let frameBytes = channels * bytesPerSample
        guard !data.isEmpty, bytesPerSample > 0, frameBytes > 0 else { return [] }
        let usable = (data.count / frameBytes) * frameBytes
        guard usable > 0 else { return [] }

        var samples: [Float32] = []
        samples.reserveCapacity(usable / frameBytes)
        data.withUnsafeBytes { raw in
            for offset in stride(from: 0, to: usable, by: frameBytes) {
                var value: Float32 = 0
                if format.isFloat {
                    if bytesPerSample == 4 {
                        value = raw.loadUnaligned(fromByteOffset: offset, as: Float32.self)
                    } else if bytesPerSample == 8 {
                        value = Float32(raw.loadUnaligned(fromByteOffset: offset, as: Float64.self))
                    }
                } else {
                    if bytesPerSample == 2 {
                        value = Float32(raw.loadUnaligned(fromByteOffset: offset, as: Int16.self)) / 32768.0
                    } else if bytesPerSample == 4 {
                        value = Float32(raw.loadUnaligned(fromByteOffset: offset, as: Int32.self)) / 2147483648.0
                    }
                }
                samples.append(value)
            }
        }
        data.removeFirst(usable)
        return samples
    }

    /// 将单声道 Float32 队列写入输出缓冲。每个 AudioBuffer 自己声明声道数，
    /// 因而同时兼容单缓冲交错与多缓冲非交错 CoreAudio 设备。
    private func writeSamples(_ samples: inout [Float32], format: DeviceFormat, into buffers: UnsafeMutablePointer<AudioBufferList>) {
        let list = UnsafeMutableAudioBufferListPointer(buffers)
        let bytesPerSample = Int(format.bits) / 8
        guard bytesPerSample > 0, !list.isEmpty else { return }
        var frameCapacity = Int.max
        for buffer in list {
            let channels = max(1, Int(buffer.mNumberChannels))
            frameCapacity = min(frameCapacity, Int(buffer.mDataByteSize) / (bytesPerSample * channels))
        }
        guard frameCapacity != Int.max else { return }
        let framesToWrite = min(samples.count, frameCapacity)

        for buffer in list {
            guard let base = buffer.mData else { continue }
            let channels = max(1, Int(buffer.mNumberChannels))
            var output = Data()
            output.reserveCapacity(frameCapacity * channels * bytesPerSample)
            for frame in 0..<frameCapacity {
                let sample = frame < framesToWrite ? samples[frame] : 0
                for _ in 0..<channels {
                    appendSample(sample, format: format, to: &output)
                }
            }
            output.withUnsafeBytes { raw in
                if let source = raw.baseAddress {
                    memcpy(base, source, min(output.count, Int(buffer.mDataByteSize)))
                }
            }
        }
        if framesToWrite > 0 {
            samples.removeFirst(framesToWrite)
        }
    }

    private func appendSample(_ sample: Float32, format: DeviceFormat, to data: inout Data) {
        let clamped = sample.isFinite ? max(-1, min(1, Double(sample))) : 0
        let bytesPerSample = Int(format.bits) / 8
        if format.isFloat {
            if bytesPerSample == 4 {
                var value = Float32(clamped)
                withUnsafeBytes(of: &value) { data.append(contentsOf: $0) }
            } else if bytesPerSample == 8 {
                var value = Float64(clamped)
                withUnsafeBytes(of: &value) { data.append(contentsOf: $0) }
            }
        } else if bytesPerSample == 2 {
            var value = Int16(clamping: Int64((clamped * Double(Int16.max)).rounded()))
            withUnsafeBytes(of: &value) { data.append(contentsOf: $0) }
        } else if bytesPerSample == 4 {
            var value = Int32(clamping: Int64((clamped * Double(Int32.max)).rounded()))
            withUnsafeBytes(of: &value) { data.append(contentsOf: $0) }
        }
    }

    private func capSamples(_ samples: inout [Float32], sampleRate: Double) {
        let maximum = max(1, Int(sampleRate * 0.5))
        if samples.count > maximum {
            samples.removeFirst(samples.count - maximum)
        }
    }

}

/// 线性插值重采样器（Float32 单声道，跨帧保持状态）
final class FloatResampler {
    private let inRate: Double
    private let outRate: Double
    private var position: Double = 0
    private var buffer: [Float32] = []

    init(inRate: Double, outRate: Double) {
        self.inRate = inRate
        self.outRate = outRate
    }

    func process(_ input: [Float32]) -> [Float32] {
        guard !input.isEmpty else { return [] }
        buffer.append(contentsOf: input)
        var out: [Float32] = []
        let ratio = inRate / outRate
        while position < Double(buffer.count - 1) {
            let i = Int(position)
            let frac = position - Double(i)
            let a = Double(buffer[i])
            let b = Double(buffer[i + 1])
            out.append(Float32(a + (b - a) * frac))
            position += ratio
        }
        let consumed = min(Int(position), buffer.count)
        if consumed > 0 {
            buffer.removeFirst(consumed)
            position -= Double(consumed)
        }
        return out
    }
}
