import Foundation
import CoreAudio
import AudioToolbox
import AVFoundation

/// 语音通话音频桥：通话中实时路由音频（含重采样、声道与位深自适应）
///  下行：模块 AC Interface（8kHz 输入）→ 重采样 → Mac 内置扬声器
///  上行：Mac 内置麦克风 → 重采样 → 模块 AS Interface（8kHz 输出）
final class AudioBridge {
    static let shared = AudioBridge()

    private struct DeviceFormat {
        var sampleRate: Double = 0
        var channels: UInt32 = 0
        var bits: UInt32 = 16
        var isFloat = false
        var isNonInterleaved = false
    }

    private var moduleInDeviceID: AudioDeviceID = 0    // AC Interface（模块输入，下行）
    private var moduleOutDeviceID: AudioDeviceID = 0   // AS Interface（模块输出，上行）
    private var speakerDeviceID: AudioDeviceID = 0
    private var micDeviceID: AudioDeviceID = 0
    private var running = false
    private let stateLock = NSLock()
    private struct IORegistration {
        let device: AudioDeviceID
        let procID: AudioDeviceIOProcID
        let context: UnsafeMutableRawPointer
    }
    private var registrations: [IORegistration] = []

    // 音频管线状态（桥锁保护）
    private var downInputBytes = Data()
    private var upInputBytes = Data()
    private var downOutputSamples: [Float32] = []
    private var upOutputSamples: [Float32] = []
    private var downResampler: FloatResampler?
    private var upResampler: FloatResampler?
    private var moduleInFormat = DeviceFormat()
    private var moduleOutFormat = DeviceFormat()
    private var speakerFormat = DeviceFormat()
    private var micFormat = DeviceFormat()
    private let bridgeLock = NSLock()

    var isRunning: Bool { stateLock.lock(); defer { stateLock.unlock() }; return running }

    func requestMicrophoneAccess() async -> Bool {
        switch AVCaptureDevice.authorizationStatus(for: .audio) {
        case .authorized:
            return true
        case .notDetermined:
            return await withCheckedContinuation { continuation in
                AVCaptureDevice.requestAccess(for: .audio) { granted in
                    continuation.resume(returning: granted)
                }
            }
        case .denied, .restricted:
            return false
        @unknown default:
            return false
        }
    }

    // MARK: - 设备发现

    private var allDevices: [AudioDeviceID] {
        var address = AudioObjectPropertyAddress(
            mSelector: kAudioHardwarePropertyDevices,
            mScope: kAudioObjectPropertyScopeGlobal,
            mElement: kAudioObjectPropertyElementMain)
        var size: UInt32 = 0
        guard AudioObjectGetPropertyDataSize(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size) == noErr else {
            return []
        }
        let count = Int(size) / MemoryLayout<AudioDeviceID>.size
        var devices = [AudioDeviceID](repeating: 0, count: count)
        guard AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, &devices) == noErr else {
            return []
        }
        return devices
    }

    private func deviceName(_ id: AudioDeviceID) -> String {
        var address = AudioObjectPropertyAddress(
            mSelector: kAudioDevicePropertyDeviceNameCFString,
            mScope: kAudioObjectPropertyScopeGlobal,
            mElement: kAudioObjectPropertyElementMain)
        var unmanagedName: Unmanaged<CFString>?
        var size = UInt32(MemoryLayout<Unmanaged<CFString>?>.size)
        guard AudioObjectGetPropertyData(id, &address, 0, nil, &size, &unmanagedName) == noErr,
              let unmanagedName else {
            return ""
        }
        return unmanagedName.takeRetainedValue() as String
    }

    private func streamFormat(device: AudioDeviceID, scope: AudioObjectPropertyScope) -> DeviceFormat? {
        var address = AudioObjectPropertyAddress(
            mSelector: kAudioDevicePropertyStreamFormat,
            mScope: scope,
            mElement: kAudioObjectPropertyElementMain)
        var format = AudioStreamBasicDescription()
        var size = UInt32(MemoryLayout<AudioStreamBasicDescription>.size)
        guard AudioObjectGetPropertyData(device, &address, 0, nil, &size, &format) == noErr else {
            return nil
        }
        let isFloat = format.mFormatFlags & kLinearPCMFormatFlagIsFloat != 0
        let isSignedInteger = format.mFormatFlags & kLinearPCMFormatFlagIsSignedInteger != 0
        let isBigEndian = format.mFormatFlags & kLinearPCMFormatFlagIsBigEndian != 0
        let supportedWidth = isFloat
            ? [32, 64].contains(Int(format.mBitsPerChannel))
            : [16, 32].contains(Int(format.mBitsPerChannel))
        guard format.mFormatID == kAudioFormatLinearPCM,
              format.mSampleRate > 0,
              format.mChannelsPerFrame > 0,
              supportedWidth,
              (isFloat || isSignedInteger),
              !isBigEndian else {
            return nil
        }
        return DeviceFormat(
            sampleRate: format.mSampleRate,
            channels: format.mChannelsPerFrame,
            bits: format.mBitsPerChannel,
            isFloat: isFloat,
            isNonInterleaved: format.mFormatFlags & kAudioFormatFlagIsNonInterleaved != 0)
    }

    func discoverDevices() -> (moduleIn: AudioDeviceID?, moduleOut: AudioDeviceID?, speaker: AudioDeviceID?, mic: AudioDeviceID?) {
        var moduleIn: AudioDeviceID?
        var moduleOut: AudioDeviceID?
        var speaker: AudioDeviceID?
        var mic: AudioDeviceID?

        for device in allDevices {
            let name = deviceName(device).lowercased()
            if moduleOut == nil && name.contains("as interface") {
                moduleOut = device
                continue
            }
            if moduleIn == nil && (name.contains("ac interface") || name.contains("baiwang") || name.contains("quectel")) {
                moduleIn = device
                continue
            }
            if speaker == nil && (name.contains("speaker") || name.contains("扬声器")) {
                speaker = device
                continue
            }
            if mic == nil && (name.contains("microphone") || name.contains("麦克风")) {
                mic = device
                continue
            }
        }
        // 兜底：系统默认输入作为麦克风
        if mic == nil {
            var address = AudioObjectPropertyAddress(
                mSelector: kAudioHardwarePropertyDefaultInputDevice,
                mScope: kAudioObjectPropertyScopeGlobal,
                mElement: kAudioObjectPropertyElementMain)
            var defaultID: AudioDeviceID = 0
            var size = UInt32(MemoryLayout<AudioDeviceID>.size)
            if AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, &defaultID) == noErr {
                mic = defaultID
            }
        }
        // 输出设备名称会随系统语言、外接显示器和用户默认设备变化；
        // 名称匹配失败时使用系统默认输出，而不是误报“未找到内置扬声器”。
        if speaker == nil {
            var address = AudioObjectPropertyAddress(
                mSelector: kAudioHardwarePropertyDefaultOutputDevice,
                mScope: kAudioObjectPropertyScopeGlobal,
                mElement: kAudioObjectPropertyElementMain)
            var defaultID: AudioDeviceID = 0
            var size = UInt32(MemoryLayout<AudioDeviceID>.size)
            if AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, &defaultID) == noErr {
                speaker = defaultID
            }
        }
        return (moduleIn, moduleOut, speaker, mic)
    }

    // MARK: - 启动/停止

    func start() -> String? {
        stateLock.lock()
        defer { stateLock.unlock() }
        guard !running else { return nil }

        let (moduleIn, moduleOut, speaker, mic) = discoverDevices()
        guard let moduleIn, moduleIn != 0 else {
            return "未找到模块音频输入设备（AC Interface）"
        }
        guard let moduleOut, moduleOut != 0 else {
            return "未找到模块音频输出设备（AS Interface）"
        }
        guard let speaker, speaker != 0 else {
            return "未找到内置扬声器"
        }
        guard let mic, mic != 0 else {
            return "未找到内置麦克风"
        }

        moduleInDeviceID = moduleIn
        moduleOutDeviceID = moduleOut
        speakerDeviceID = speaker
        micDeviceID = mic

        guard let detectedModuleInFormat = streamFormat(device: moduleIn, scope: kAudioObjectPropertyScopeInput),
              let detectedModuleOutFormat = streamFormat(device: moduleOut, scope: kAudioObjectPropertyScopeOutput),
              let detectedSpeakerFormat = streamFormat(device: speaker, scope: kAudioObjectPropertyScopeOutput),
              let detectedMicFormat = streamFormat(device: mic, scope: kAudioObjectPropertyScopeInput) else {
            return "音频设备不是受支持的原生端序 16/32 位整数或 32/64 位浮点 PCM 格式"
        }
        moduleInFormat = detectedModuleInFormat
        moduleOutFormat = detectedModuleOutFormat
        speakerFormat = detectedSpeakerFormat
        micFormat = detectedMicFormat

        downResampler = FloatResampler(inRate: max(1, moduleInFormat.sampleRate), outRate: max(1, speakerFormat.sampleRate))
        upResampler = FloatResampler(inRate: max(1, micFormat.sampleRate), outRate: max(1, moduleOutFormat.sampleRate))

        guard registerIOProcs() else {
            unregisterIOProcs()
            return "音频设备注册失败"
        }
        running = true
        return nil
    }

    func stop() {
        stateLock.lock()
        defer { stateLock.unlock() }
        guard running else { return }
        unregisterIOProcs()
        bridgeLock.lock()
        downInputBytes.removeAll()
        upInputBytes.removeAll()
        downOutputSamples.removeAll()
        upOutputSamples.removeAll()
        downResampler = nil
        upResampler = nil
        bridgeLock.unlock()
        running = false
    }

    // MARK: - IOProc

    private func registerIOProcs() -> Bool {
        let registrations: [(AudioDeviceID, Int)] = [
            (moduleInDeviceID, 0),    // 模块输入（下行数据源）
            (moduleOutDeviceID, 1),   // 模块输出（上行目标）
            (speakerDeviceID, 2),     // 扬声器（下行播放目标）
            (micDeviceID, 3),         // 麦克风（上行数据源）
        ]
        for (device, role) in registrations {
            var procID: AudioDeviceIOProcID?
            let context = UnsafeMutableRawPointer(Unmanaged.passRetained(BridgeContext(role: role)).toOpaque())
            let err = AudioDeviceCreateIOProcID(device, { _, _, inInputData, _, outOutputData, _, inClientData -> OSStatus in
                guard let inClientData else { return noErr }
                let context = Unmanaged<BridgeContext>.fromOpaque(inClientData).takeUnretainedValue()
                return AudioBridge.shared.ioProc(role: context.role, input: inInputData, output: outOutputData)
            }, context, &procID)
            if err != noErr {
                Unmanaged<BridgeContext>.fromOpaque(context).release()
                return false
            }
            guard let procID else {
                Unmanaged<BridgeContext>.fromOpaque(context).release()
                return false
            }
            self.registrations.append(IORegistration(device: device, procID: procID, context: context))
            if AudioDeviceStart(device, procID) != noErr {
                return false
            }
        }
        return true
    }

    private func unregisterIOProcs() {
        for registration in registrations.reversed() {
            AudioDeviceStop(registration.device, registration.procID)
            AudioDeviceDestroyIOProcID(registration.device, registration.procID)
            Unmanaged<BridgeContext>.fromOpaque(registration.context).release()
        }
        registrations.removeAll()
    }

    private func ioProc(role: Int, input: UnsafePointer<AudioBufferList>?, output: UnsafeMutablePointer<AudioBufferList>?) -> OSStatus {
        bridgeLock.lock()
        defer { bridgeLock.unlock() }

        switch role {
        case 0: // 模块输入：读下行（对方语音）
            if let input {
                appendBufferList(input, to: &downInputBytes)
            }
        case 1: // 模块输出：写上行（发给网络）
            if let output {
                let samples = takeSamples(from: &upInputBytes, format: micFormat, toMono: true)
                let resampled = upResampler?.process(samples) ?? samples
                upOutputSamples.append(contentsOf: resampled)
                capSamples(&upOutputSamples, sampleRate: moduleOutFormat.sampleRate)
                writeSamples(&upOutputSamples, format: moduleOutFormat, into: output)
            }
        case 2: // 扬声器：写下行（播放对方语音）
            if let output {
                let samples = takeSamples(from: &downInputBytes, format: moduleInFormat, toMono: true)
                let resampled = downResampler?.process(samples) ?? samples
                downOutputSamples.append(contentsOf: resampled)
                capSamples(&downOutputSamples, sampleRate: speakerFormat.sampleRate)
                writeSamples(&downOutputSamples, format: speakerFormat, into: output)
            }
        case 3: // 麦克风：读上行（我说话）
            if let input {
                appendBufferList(input, to: &upInputBytes)
            }
        default:
            break
        }
        return noErr
    }

    // MARK: - 音频数据处理

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

    /// 从字节缓冲读取样本（按格式位深/浮点解析，可选取单声道），返回 Float32
    private func takeSamples(from data: inout Data, format: DeviceFormat, toMono: Bool) -> [Float32] {
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
        samples.reserveCapacity(usable / bytesPerSample / (toMono ? channels : 1))
        data.withUnsafeBytes { raw in
            let total = usable / bytesPerSample
            for i in 0..<total {
                let offset = i * bytesPerSample
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
                if toMono && channels > 1 {
                    if i % channels == 0 {
                        samples.append(value)
                    }
                } else {
                    samples.append(value)
                }
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
        let clamped = max(-1, min(1, sample))
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
            var value = Int16(clamped * 32767.0)
            withUnsafeBytes(of: &value) { data.append(contentsOf: $0) }
        } else if bytesPerSample == 4 {
            var value = Int32(clamped * 2147483647.0)
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
        let consumed = Int(position)
        if consumed > 0 {
            buffer.removeFirst(min(consumed, buffer.count))
            position -= Double(consumed)
        }
        return out
    }
}

private final class BridgeContext {
    let role: Int
    init(role: Int) {
        self.role = role
    }
}
