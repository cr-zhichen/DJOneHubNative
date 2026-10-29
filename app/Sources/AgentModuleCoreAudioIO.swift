import Foundation
import CoreAudio
import CryptoKit

// Socket frames are fixed PCM16LE while CoreAudio commonly exposes Float32.
enum AgentModulePCMCodec {
    static func decodePCM16LE(_ data: Data) -> [Float32] {
        guard data.count.isMultiple(of: 2) else { return [] }
        return data.withUnsafeBytes { raw in
            stride(from: 0, to: raw.count, by: 2).map { offset in
                Float32(raw.loadUnaligned(fromByteOffset: offset, as: Int16.self)) / 32768.0
            }
        }
    }

    static func encodePCM16LE(_ samples: [Float32]) -> Data {
        var result = Data()
        result.reserveCapacity(samples.count * 2)
        for sample in samples {
            let finite = sample.isFinite ? sample : 0
            let clamped = max(-1, min(1, finite))
            let scaledValue = max(Float32(Int16.min), min(Float32(Int16.max), (clamped * 32768).rounded()))
            let scaled = Int16(scaledValue)
            var little = scaled.littleEndian
            withUnsafeBytes(of: &little) { result.append(contentsOf: $0) }
        }
        return result
    }
}

struct AgentModulePCMAccumulator {
    private(set) var samples: [Float32] = []
    let frameSamples: Int
    var bufferedSamples: Int { samples.count }

    init(frameSamples: Int) {
        precondition(frameSamples > 0)
        self.frameSamples = frameSamples
    }

    mutating func append(_ incoming: [Float32]) -> [Data] {
        samples.append(contentsOf: incoming)
        var frames: [Data] = []
        while samples.count >= frameSamples {
            frames.append(AgentModulePCMCodec.encodePCM16LE(Array(samples.prefix(frameSamples))))
            samples.removeFirst(frameSamples)
        }
        return frames
    }

    mutating func reset() { samples.removeAll(keepingCapacity: false) }
}

enum AgentModuleIODirection {
    case capture
    case playback
}

struct AgentModuleIOReadiness {
    private var captureStarted = false
    private var playbackStarted = false
    private var captureCallback = false
    private var playbackCallback = false
    private(set) var isReady = false

    mutating func beginStart() { reset() }

    mutating func markStarted(_ direction: AgentModuleIODirection) {
        switch direction {
        case .capture: captureStarted = true
        case .playback: playbackStarted = true
        }
        updateReady()
    }

    @discardableResult
    mutating func markCallback(_ direction: AgentModuleIODirection) -> Bool {
        switch direction {
        case .capture: captureCallback = true
        case .playback: playbackCallback = true
        }
        let wasReady = isReady
        updateReady()
        return !wasReady && isReady
    }

    mutating func reset() {
        captureStarted = false
        playbackStarted = false
        captureCallback = false
        playbackCallback = false
        isReady = false
    }

    private mutating func updateReady() {
        isReady = captureStarted && playbackStarted && captureCallback && playbackCallback
    }
}

enum AgentModuleNativeSampleKind {
    case float32
    case int16
    case int32
}

struct AgentModuleNativePCMFormat {
    let sampleRate: Double
    let channels: Int
    let kind: AgentModuleNativeSampleKind
    let isNonInterleaved: Bool

    var bits: Int {
        switch kind {
        case .float32, .int32: return 32
        case .int16: return 16
        }
    }

    init?(_ format: AudioStreamBasicDescription, channels: Int) {
        guard channels == 1,
              format.mFormatID == kAudioFormatLinearPCM,
              format.mSampleRate == Double(AgentAudioProtocol.sampleRate),
              format.mFormatFlags & kAudioFormatFlagIsBigEndian == 0,
              format.mFormatFlags & kAudioFormatFlagIsPacked != 0 else { return nil }
        let isFloat = format.mFormatFlags & kLinearPCMFormatFlagIsFloat != 0
        let isSigned = format.mFormatFlags & kLinearPCMFormatFlagIsSignedInteger != 0
        let kind: AgentModuleNativeSampleKind
        switch (isFloat, isSigned, format.mBitsPerChannel) {
        case (true, false, 32): kind = .float32
        case (false, true, 16): kind = .int16
        case (false, true, 32): kind = .int32
        default: return nil
        }
        let bytesPerSample = Int(format.mBitsPerChannel / 8)
        let streamChannels = format.mFormatFlags & kAudioFormatFlagIsNonInterleaved != 0 ? 1 : channels
        guard bytesPerSample > 0, Int(format.mBytesPerFrame) == bytesPerSample * streamChannels else { return nil }
        self.sampleRate = format.mSampleRate
        self.channels = channels
        self.kind = kind
        self.isNonInterleaved = format.mFormatFlags & kAudioFormatFlagIsNonInterleaved != 0
    }
}

struct AgentModuleEndpointCandidate {
    let id: AudioDeviceID
    let name: String
    let family: String
    let input: AgentModuleNativePCMFormat?
    let output: AgentModuleNativePCMFormat?
}

enum AgentModuleEndpointSelector {
    static func resolve(_ candidates: [AgentModuleEndpointCandidate]) ->
        (input: AgentModuleEndpointCandidate, output: AgentModuleEndpointCandidate)? {
        for input in candidates where input.input != nil {
            if let output = candidates.first(where: { $0.output != nil && ($0.id == input.id || $0.family == input.family) }) {
                return (input, output)
            }
        }
        return nil
    }
}

struct AgentModuleAudioDeviceInfo: Equatable {
    let uidHash: String
    let name: String
    let inputChannels: UInt32
    let outputChannels: UInt32
    let sampleRate: Double
    let bitsPerChannel: UInt32
    let isPCM: Bool
    let likelyModule: Bool
}

enum AgentModuleCoreAudioDiscovery {
    static func list() -> [AgentModuleAudioDeviceInfo] {
        deviceIDs().compactMap { inspect($0) }.sorted {
            $0.name.localizedStandardCompare($1.name) == .orderedAscending
        }
    }

    static func likelyModuleDevices() -> [AgentModuleAudioDeviceInfo] {
        list().filter(\.likelyModule)
    }

    private static func inspect(_ id: AudioDeviceID) -> AgentModuleAudioDeviceInfo? {
        guard uintProperty(id, kAudioDevicePropertyDeviceIsAlive) == 1,
              uintProperty(id, kAudioDevicePropertyIsHidden) != 1 else { return nil }
        let name = stringProperty(id, kAudioObjectPropertyName)
        let uid = stringProperty(id, kAudioDevicePropertyDeviceUID)
        guard !name.isEmpty, !uid.isEmpty else { return nil }
        let input = streamInfo(id, scope: kAudioObjectPropertyScopeInput)
        let output = streamInfo(id, scope: kAudioObjectPropertyScopeOutput)
        guard input.channels > 0 || output.channels > 0 else { return nil }
        let identity = "\(uid) \(name)".lowercased()
        let likelyModule = ["ac interface", "as interface", "baiwang", "百旺", "quectel", "qdc507"]
            .contains { identity.contains($0) }
        let format = input.sampleRate > 0 ? input : output
        return AgentModuleAudioDeviceInfo(
            uidHash: hash(uid), name: name,
            inputChannels: input.channels, outputChannels: output.channels,
            sampleRate: format.sampleRate, bitsPerChannel: format.bits,
            isPCM: format.isPCM, likelyModule: likelyModule
        )
    }

    private struct StreamInfo {
        var channels: UInt32 = 0
        var sampleRate: Double = 0
        var bits: UInt32 = 0
        var isPCM = false
    }

    private static func streamInfo(_ id: AudioDeviceID, scope: AudioObjectPropertyScope) -> StreamInfo {
        var result = StreamInfo()
        var configAddress = address(kAudioDevicePropertyStreamConfiguration, scope: scope)
        var size: UInt32 = 0
        guard AudioObjectGetPropertyDataSize(id, &configAddress, 0, nil, &size) == noErr,
              size >= MemoryLayout<AudioBufferList>.size else { return result }
        let memory = UnsafeMutableRawPointer.allocate(byteCount: Int(size), alignment: MemoryLayout<AudioBufferList>.alignment)
        defer { memory.deallocate() }
        guard AudioObjectGetPropertyData(id, &configAddress, 0, nil, &size, memory) == noErr else { return result }
        let buffers = UnsafeMutableAudioBufferListPointer(memory.assumingMemoryBound(to: AudioBufferList.self))
        result.channels = buffers.reduce(0) { $0 + $1.mNumberChannels }
        guard result.channels > 0 else { return result }

        var formatAddress = address(kAudioDevicePropertyStreamFormat, scope: scope)
        var format = AudioStreamBasicDescription()
        var formatSize = UInt32(MemoryLayout<AudioStreamBasicDescription>.size)
        guard AudioObjectGetPropertyData(id, &formatAddress, 0, nil, &formatSize, &format) == noErr else { return result }
        result.sampleRate = format.mSampleRate
        result.bits = format.mBitsPerChannel
        let floatPCM = format.mFormatID == kAudioFormatLinearPCM && format.mFormatFlags & kLinearPCMFormatFlagIsFloat != 0
        let signedPCM = format.mFormatID == kAudioFormatLinearPCM && format.mFormatFlags & kLinearPCMFormatFlagIsSignedInteger != 0
        result.isPCM = (floatPCM || signedPCM) && format.mSampleRate > 0 && format.mBitsPerChannel > 0
        return result
    }

    private static func deviceIDs() -> [AudioDeviceID] {
        let system = AudioObjectID(kAudioObjectSystemObject)
        var property = address(kAudioHardwarePropertyDevices)
        var size: UInt32 = 0
        guard AudioObjectGetPropertyDataSize(system, &property, 0, nil, &size) == noErr else { return [] }
        var ids = [AudioDeviceID](repeating: 0, count: Int(size) / MemoryLayout<AudioDeviceID>.size)
        guard AudioObjectGetPropertyData(system, &property, 0, nil, &size, &ids) == noErr else { return [] }
        return ids
    }

    private static func address(_ selector: AudioObjectPropertySelector,
                               scope: AudioObjectPropertyScope = kAudioObjectPropertyScopeGlobal) -> AudioObjectPropertyAddress {
        AudioObjectPropertyAddress(mSelector: selector, mScope: scope, mElement: kAudioObjectPropertyElementMain)
    }

    private static func stringProperty(_ id: AudioObjectID, _ selector: AudioObjectPropertySelector) -> String {
        var property = address(selector)
        var value: Unmanaged<CFString>?
        var size = UInt32(MemoryLayout<Unmanaged<CFString>?>.size)
        guard AudioObjectGetPropertyData(id, &property, 0, nil, &size, &value) == noErr,
              let value else { return "" }
        return value.takeRetainedValue() as String
    }

    private static func uintProperty(_ id: AudioObjectID, _ selector: AudioObjectPropertySelector) -> UInt32? {
        var property = address(selector)
        var value: UInt32 = 0
        var size = UInt32(MemoryLayout<UInt32>.size)
        guard AudioObjectGetPropertyData(id, &property, 0, nil, &size, &value) == noErr else { return nil }
        return value
    }

    private static func hash(_ value: String) -> String {
        SHA256.hash(data: Data(value.utf8)).map { String(format: "%02x", $0) }.joined()
    }
}

/// Real QDC507 UAC adapter. It opens only the module's 8 kHz PCM endpoints and
/// reports ready after CoreAudio delivers the first input/output callback.
final class CoreAudioAgentModuleAudioIO: AgentModuleAudioIO, @unchecked Sendable {
    var onReady: (() -> Void)?
    var onUnavailable: (() -> Void)?
    var onCapturePCM: ((Data) -> Void)?

    private struct Registration {
        let device: AudioDeviceID
        let procID: AudioDeviceIOProcID
        let context: UnsafeMutableRawPointer
    }
    private struct AliveListener {
        let device: AudioDeviceID
        var address: AudioObjectPropertyAddress
        let block: AudioObjectPropertyListenerBlock
    }
    private final class IOContext {
        unowned let owner: CoreAudioAgentModuleAudioIO
        let direction: AgentModuleIODirection
        let format: AgentModuleNativePCMFormat
        init(owner: CoreAudioAgentModuleAudioIO, direction: AgentModuleIODirection,
             format: AgentModuleNativePCMFormat) {
            self.owner = owner
            self.direction = direction
            self.format = format
        }
    }
    private struct ModuleIOError: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    private let lifecycleLock = NSLock()
    private let ioLock = NSLock()
    private let listenerQueue = DispatchQueue(label: "com.djonehub.agent-module-audio.alive")
    private var registrations: [Registration] = []
    private var aliveListeners: [AliveListener] = []
    private var pullDownlink: (() -> Data)?
    private var pushUplink: ((Data) -> Void)?
    private var captureAccumulator = AgentModulePCMAccumulator(frameSamples: 160)
    private var playbackSamples: [Float32] = []
    private var readiness = AgentModuleIOReadiness()
    private var starting = false
    private var readyDelivered = false
    private var active = false

    func start(pullDownlink: @escaping () -> Data, pushUplink: @escaping (Data) -> Void) throws {
        lifecycleLock.lock()
        guard registrations.isEmpty else {
            lifecycleLock.unlock()
            throw ModuleIOError(message: "Agent module audio is already running")
        }
        do {
            let pair = try Self.resolveModuleEndpoints()
            ioLock.lock()
            self.pullDownlink = pullDownlink
            self.pushUplink = pushUplink
            captureAccumulator.reset()
            playbackSamples.removeAll(keepingCapacity: false)
            readiness.beginStart()
            starting = true
            readyDelivered = false
            active = true
            ioLock.unlock()

            // QDC507's AC input endpoint can remain blocked until the AS output
            // endpoint has opened the module's USB audio clock/route. Start
            // playback first, then capture; readiness still requires callbacks
            // from both directions.
            try register(pair.output.id, name: pair.output.name, format: pair.output.output!, direction: .playback)
            try register(pair.input.id, name: pair.input.name, format: pair.input.input!, direction: .capture)
            try installAliveListeners(for: Set([pair.input.id, pair.output.id]))

            ioLock.lock()
            starting = false
            let readyCallback = takeReadyCallbackLocked()
            ioLock.unlock()
            lifecycleLock.unlock()
            readyCallback?()
        } catch {
            stopRegistrations()
            removeAliveListeners()
            ioLock.lock()
            resetIOLocked()
            ioLock.unlock()
            lifecycleLock.unlock()
            throw error
        }
    }

    func stop() {
        lifecycleLock.lock()
        stopRegistrations()
        removeAliveListeners()
        ioLock.lock()
        resetIOLocked()
        ioLock.unlock()
        lifecycleLock.unlock()
    }

    private func register(_ device: AudioDeviceID, name: String, format: AgentModuleNativePCMFormat,
                          direction: AgentModuleIODirection) throws {
        let context = IOContext(owner: self, direction: direction, format: format)
        let pointer = Unmanaged.passRetained(context).toOpaque()
        var procID: AudioDeviceIOProcID?
        let status = AudioDeviceCreateIOProcID(device, { _, _, input, _, output, _, pointer in
            guard let pointer else { return noErr }
            let context = Unmanaged<IOContext>.fromOpaque(pointer).takeUnretainedValue()
            context.owner.process(input: input, output: output, context: context)
            return noErr
        }, pointer, &procID)
        guard status == noErr, let procID else {
            Unmanaged<IOContext>.fromOpaque(pointer).release()
            throw ModuleIOError(message: "CoreAudio IOProc registration failed for \(name) (\(status))")
        }
        registrations.append(Registration(device: device, procID: procID, context: pointer))
        let startStatus = AudioDeviceStart(device, procID)
        guard startStatus == noErr else {
            throw ModuleIOError(message: "CoreAudio device start failed for \(name) (\(startStatus))")
        }
        ioLock.lock()
        readiness.markStarted(direction)
        ioLock.unlock()
    }

    private func stopRegistrations() {
        for registration in registrations.reversed() {
            AudioDeviceStop(registration.device, registration.procID)
            AudioDeviceDestroyIOProcID(registration.device, registration.procID)
            Unmanaged<IOContext>.fromOpaque(registration.context).release()
        }
        registrations.removeAll()
    }

    private func installAliveListeners(for devices: Set<AudioDeviceID>) throws {
        for device in devices {
            var address = Self.propertyAddress(kAudioDevicePropertyDeviceIsAlive)
            let block: AudioObjectPropertyListenerBlock = { [weak self] _, _ in
                guard Self.uintProperty(device, kAudioDevicePropertyDeviceIsAlive) != 1 else { return }
                self?.deviceBecameUnavailable()
            }
            let status = AudioObjectAddPropertyListenerBlock(device, &address, listenerQueue, block)
            guard status == noErr else {
                throw ModuleIOError(message: "CoreAudio alive listener failed (\(status))")
            }
            aliveListeners.append(AliveListener(device: device, address: address, block: block))
            guard Self.uintProperty(device, kAudioDevicePropertyDeviceIsAlive) == 1 else {
                throw ModuleIOError(message: "CoreAudio module endpoint became unavailable during start")
            }
        }
    }

    private func removeAliveListeners() {
        for var listener in aliveListeners.reversed() {
            AudioObjectRemovePropertyListenerBlock(listener.device, &listener.address, listenerQueue, listener.block)
        }
        aliveListeners.removeAll()
    }

    private func deviceBecameUnavailable() {
        ioLock.lock()
        guard active else {
            ioLock.unlock()
            return
        }
        active = false
        readiness.reset()
        readyDelivered = false
        let callback = onUnavailable
        ioLock.unlock()
        callback?()
    }

    private func resetIOLocked() {
        pullDownlink = nil
        pushUplink = nil
        captureAccumulator.reset()
        playbackSamples.removeAll(keepingCapacity: false)
        readiness.reset()
        starting = false
        readyDelivered = false
        active = false
    }

    private func takeReadyCallbackLocked() -> (() -> Void)? {
        guard active, !starting, readiness.isReady, !readyDelivered else { return nil }
        readyDelivered = true
        return onReady
    }

    private func process(input: UnsafePointer<AudioBufferList>?, output: UnsafeMutablePointer<AudioBufferList>?,
                         context: IOContext) {
        switch context.direction {
        case .capture:
            guard let input else { return }
            let samples = Self.readSamples(input, format: context.format)
            guard ioLock.try() else { return }
            let emitted = captureAccumulator.append(samples)
            _ = readiness.markCallback(.capture)
            let push = pushUplink
            let capture = onCapturePCM
            let readyCallback = takeReadyCallbackLocked()
            ioLock.unlock()
            for frame in emitted {
                push?(frame)
                capture?(frame)
            }
            readyCallback?()

        case .playback:
            guard let output else { return }
            Self.silence(output)
            let capacity = Self.frameCapacity(output, format: context.format)
            guard capacity > 0, ioLock.try() else { return }
            let missing = max(0, capacity - playbackSamples.count)
            let pull = pullDownlink
            ioLock.unlock()

            let framesNeeded = (missing + 159) / 160
            var fetched: [Float32] = []
            fetched.reserveCapacity(framesNeeded * 160)
            for _ in 0..<framesNeeded {
                let pcm = pull?() ?? Data(count: AgentAudioProtocol.pcmFrameBytes)
                guard pcm.count == AgentAudioProtocol.pcmFrameBytes else { continue }
                fetched.append(contentsOf: AgentModulePCMCodec.decodePCM16LE(pcm))
            }

            guard ioLock.try() else { return }
            playbackSamples.append(contentsOf: fetched)
            Self.writeSamples(&playbackSamples, output: output, format: context.format)
            _ = readiness.markCallback(.playback)
            let readyCallback = takeReadyCallbackLocked()
            ioLock.unlock()
            readyCallback?()
        }
    }

    private static func silence(_ output: UnsafeMutablePointer<AudioBufferList>) {
        for buffer in UnsafeMutableAudioBufferListPointer(output) {
            if let data = buffer.mData { memset(data, 0, Int(buffer.mDataByteSize)) }
        }
    }

    private static func readSamples(_ input: UnsafePointer<AudioBufferList>,
                                    format: AgentModuleNativePCMFormat) -> [Float32] {
        let list = UnsafeMutableAudioBufferListPointer(UnsafeMutablePointer(mutating: input))
        guard let buffer = list.first, let base = buffer.mData else { return [] }
        let bytesPerSample = format.bits / 8
        let channels = format.isNonInterleaved ? 1 : max(1, format.channels)
        let strideBytes = bytesPerSample * channels
        guard strideBytes > 0 else { return [] }
        let frames = Int(buffer.mDataByteSize) / strideBytes
        let raw = UnsafeRawBufferPointer(start: base, count: Int(buffer.mDataByteSize))
        return (0..<frames).map { frame in
            let offset = frame * strideBytes
            switch format.kind {
            case .float32:
                return raw.loadUnaligned(fromByteOffset: offset, as: Float32.self)
            case .int16:
                return Float32(raw.loadUnaligned(fromByteOffset: offset, as: Int16.self)) / 32768.0
            case .int32:
                return Float32(raw.loadUnaligned(fromByteOffset: offset, as: Int32.self)) / 2147483648.0
            }
        }
    }

    private static func frameCapacity(_ output: UnsafeMutablePointer<AudioBufferList>,
                                      format: AgentModuleNativePCMFormat) -> Int {
        let bytesPerSample = format.bits / 8
        return UnsafeMutableAudioBufferListPointer(output).map { buffer in
            Int(buffer.mDataByteSize) / (bytesPerSample * max(1, Int(buffer.mNumberChannels)))
        }.min() ?? 0
    }

    private static func writeSamples(_ samples: inout [Float32], output: UnsafeMutablePointer<AudioBufferList>,
                                     format: AgentModuleNativePCMFormat) {
        let list = UnsafeMutableAudioBufferListPointer(output)
        let frames = min(samples.count, frameCapacity(output, format: format))
        let bytesPerSample = format.bits / 8
        for buffer in list {
            guard let base = buffer.mData else { continue }
            let channels = max(1, Int(buffer.mNumberChannels))
            let raw = UnsafeMutableRawBufferPointer(start: base, count: Int(buffer.mDataByteSize))
            for frame in 0..<frames {
                let value = max(-1, min(1, samples[frame].isFinite ? samples[frame] : 0))
                for channel in 0..<channels {
                    let offset = (frame * channels + channel) * bytesPerSample
                    switch format.kind {
                    case .float32:
                        raw.storeBytes(of: value, toByteOffset: offset, as: Float32.self)
                    case .int16:
                        let scaled = Int16(max(Float32(Int16.min), min(Float32(Int16.max), (value * 32768).rounded())))
                        raw.storeBytes(of: scaled, toByteOffset: offset, as: Int16.self)
                    case .int32:
                        let scaled = Int32(max(Float64(Int32.min),
                                               min(Float64(Int32.max), (Double(value) * 2147483648).rounded())))
                        raw.storeBytes(of: scaled, toByteOffset: offset, as: Int32.self)
                    }
                }
            }
        }
        if frames > 0 { samples.removeFirst(frames) }
    }

    private static func resolveModuleEndpoints() throws ->
        (input: AgentModuleEndpointCandidate, output: AgentModuleEndpointCandidate) {
        var candidates: [AgentModuleEndpointCandidate] = []
        for id in deviceIDs() {
            guard uintProperty(id, kAudioDevicePropertyDeviceIsAlive) == 1,
                  uintProperty(id, kAudioDevicePropertyIsHidden) != 1 else { continue }
            let name = stringProperty(id, kAudioObjectPropertyName)
            let uid = stringProperty(id, kAudioDevicePropertyDeviceUID)
            guard !name.isEmpty, !uid.isEmpty else { continue }
            let identity = "\(uid) \(name)".lowercased()
            guard ["ac interface", "as interface", "baiwang", "百旺", "quectel", "qdc507"]
                .contains(where: identity.contains) else { continue }
            let input = nativeFormat(id, scope: kAudioObjectPropertyScopeInput)
            let output = nativeFormat(id, scope: kAudioObjectPropertyScopeOutput)
            guard input != nil || output != nil else { continue }
            candidates.append(AgentModuleEndpointCandidate(id: id, name: name,
                family: familyKey(uid: uid), input: input, output: output))
        }
        guard let pair = AgentModuleEndpointSelector.resolve(candidates) else {
            throw ModuleIOError(message: "Compatible QDC507 8 kHz CoreAudio input/output endpoints from one module family not found")
        }
        return pair
    }

    private static func familyKey(uid: String) -> String {
        let value = uid.lowercased().trimmingCharacters(in: .whitespacesAndNewlines)
        let components = value.split(separator: ":", omittingEmptySubsequences: false)
        if components.count >= 3 {
            return components.dropLast().joined(separator: ":")
        }
        return value
            .replacingOccurrences(of: "ac interface", with: "interface")
            .replacingOccurrences(of: "as interface", with: "interface")
    }

    private static func nativeFormat(_ id: AudioDeviceID,
                                     scope: AudioObjectPropertyScope) -> AgentModuleNativePCMFormat? {
        var configuration = propertyAddress(kAudioDevicePropertyStreamConfiguration, scope: scope)
        var configSize: UInt32 = 0
        guard AudioObjectGetPropertyDataSize(id, &configuration, 0, nil, &configSize) == noErr,
              configSize >= MemoryLayout<AudioBufferList>.size else { return nil }
        let memory = UnsafeMutableRawPointer.allocate(byteCount: Int(configSize),
            alignment: MemoryLayout<AudioBufferList>.alignment)
        defer { memory.deallocate() }
        guard AudioObjectGetPropertyData(id, &configuration, 0, nil, &configSize, memory) == noErr else { return nil }
        let channels = UnsafeMutableAudioBufferListPointer(memory.assumingMemoryBound(to: AudioBufferList.self))
            .reduce(0) { $0 + Int($1.mNumberChannels) }
        guard channels > 0 else { return nil }
        var formatAddress = propertyAddress(kAudioDevicePropertyStreamFormat, scope: scope)
        var format = AudioStreamBasicDescription()
        var size = UInt32(MemoryLayout<AudioStreamBasicDescription>.size)
        guard AudioObjectGetPropertyData(id, &formatAddress, 0, nil, &size, &format) == noErr else { return nil }
        return AgentModuleNativePCMFormat(format, channels: channels)
    }

    private static func deviceIDs() -> [AudioDeviceID] {
        let system = AudioObjectID(kAudioObjectSystemObject)
        var property = propertyAddress(kAudioHardwarePropertyDevices)
        var size: UInt32 = 0
        guard AudioObjectGetPropertyDataSize(system, &property, 0, nil, &size) == noErr else { return [] }
        var ids = [AudioDeviceID](repeating: 0, count: Int(size) / MemoryLayout<AudioDeviceID>.size)
        guard AudioObjectGetPropertyData(system, &property, 0, nil, &size, &ids) == noErr else { return [] }
        return ids
    }

    private static func propertyAddress(_ selector: AudioObjectPropertySelector,
                                        scope: AudioObjectPropertyScope = kAudioObjectPropertyScopeGlobal)
        -> AudioObjectPropertyAddress {
        AudioObjectPropertyAddress(mSelector: selector, mScope: scope,
                                   mElement: kAudioObjectPropertyElementMain)
    }

    private static func stringProperty(_ id: AudioObjectID, _ selector: AudioObjectPropertySelector) -> String {
        var property = propertyAddress(selector)
        var value: Unmanaged<CFString>?
        var size = UInt32(MemoryLayout<Unmanaged<CFString>?>.size)
        guard AudioObjectGetPropertyData(id, &property, 0, nil, &size, &value) == noErr,
              let value else { return "" }
        return value.takeRetainedValue() as String
    }

    private static func uintProperty(_ id: AudioObjectID,
                                     _ selector: AudioObjectPropertySelector) -> UInt32? {
        var property = propertyAddress(selector)
        var value: UInt32 = 0
        var size = UInt32(MemoryLayout<UInt32>.size)
        guard AudioObjectGetPropertyData(id, &property, 0, nil, &size, &value) == noErr else { return nil }
        return value
    }
}
