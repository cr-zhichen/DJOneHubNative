import Foundation
import CoreAudio

func expect(_ condition: @autoclosure () -> Bool, _ message: String) {
    if !condition() { fatalError(message) }
}

func testPCM16FloatRoundTrip() {
    let pcm = Data([0x00, 0x80, 0x00, 0x00, 0xff, 0x7f])
    let floats = AgentModulePCMCodec.decodePCM16LE(pcm)
    expect(floats.count == 3, "sample count")
    expect(floats[0] == -1.0, "negative full scale")
    expect(floats[1] == 0, "zero")
    expect(floats[2] > 0.999, "positive full scale")
    let encoded = AgentModulePCMCodec.encodePCM16LE(floats)
    expect(encoded == pcm, "round trip")
    print("PASS module PCM16/Float32 conversion")
}

func testFrameAccumulator() {
    var accumulator = AgentModulePCMAccumulator(frameSamples: 160)
    expect(accumulator.append(Array(repeating: 0.25, count: 80)).isEmpty, "half frame emitted")
    let frames = accumulator.append(Array(repeating: -0.25, count: 240))
    expect(frames.count == 2, "expected two complete frames")
    expect(frames.allSatisfy { $0.count == 320 }, "frame bytes")
    expect(accumulator.bufferedSamples == 0, "leftover samples")
    print("PASS module 20ms frame accumulation")
}

func testSupportedNativeFormats() {
    func format(rate: Double = 8_000, bits: UInt32, flags: AudioFormatFlags, bytes: UInt32) -> AudioStreamBasicDescription {
        AudioStreamBasicDescription(mSampleRate: rate, mFormatID: kAudioFormatLinearPCM,
                                    mFormatFlags: flags, mBytesPerPacket: bytes,
                                    mFramesPerPacket: 1, mBytesPerFrame: bytes,
                                    mChannelsPerFrame: 1, mBitsPerChannel: bits, mReserved: 0)
    }
    expect(AgentModuleNativePCMFormat(format(bits: 32, flags: kLinearPCMFormatFlagIsFloat | kAudioFormatFlagIsPacked, bytes: 4), channels: 1) != nil,
           "Float32 must be supported")
    expect(AgentModuleNativePCMFormat(format(bits: 16, flags: kLinearPCMFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked, bytes: 2), channels: 1) != nil,
           "Int16 must be supported")
    expect(AgentModuleNativePCMFormat(format(bits: 32, flags: kLinearPCMFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked, bytes: 4), channels: 1) != nil,
           "Int32 must be supported")
    expect(AgentModuleNativePCMFormat(format(bits: 24, flags: kLinearPCMFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked, bytes: 3), channels: 1) == nil,
           "24-bit PCM must be rejected")
    expect(AgentModuleNativePCMFormat(format(rate: 48_000, bits: 32, flags: kLinearPCMFormatFlagIsFloat | kAudioFormatFlagIsPacked, bytes: 4), channels: 1) == nil,
           "non-8k PCM must be rejected")
    let stereo = AudioStreamBasicDescription(mSampleRate: 8_000, mFormatID: kAudioFormatLinearPCM,
        mFormatFlags: kLinearPCMFormatFlagIsFloat | kAudioFormatFlagIsPacked,
        mBytesPerPacket: 8, mFramesPerPacket: 1, mBytesPerFrame: 8,
        mChannelsPerFrame: 2, mBitsPerChannel: 32, mReserved: 0)
    expect(AgentModuleNativePCMFormat(stereo, channels: 2) == nil,
           "multi-channel PCM must be rejected until an explicit downmix policy exists")
    print("PASS module native format allowlist rejects unsupported PCM")
}

func testEndpointPairingStaysWithinFamily() {
    let description = AudioStreamBasicDescription(mSampleRate: 8_000, mFormatID: kAudioFormatLinearPCM,
        mFormatFlags: kLinearPCMFormatFlagIsFloat | kAudioFormatFlagIsPacked,
        mBytesPerPacket: 4, mFramesPerPacket: 1, mBytesPerFrame: 4,
        mChannelsPerFrame: 1, mBitsPerChannel: 32, mReserved: 0)
    let pcm = AgentModuleNativePCMFormat(description, channels: 1)!
    let candidates = [
        AgentModuleEndpointCandidate(id: 1, name: "AC Interface invalid", family: "module-a", input: nil, output: nil),
        AgentModuleEndpointCandidate(id: 2, name: "AC Interface A", family: "module-a", input: pcm, output: nil),
        AgentModuleEndpointCandidate(id: 3, name: "AS Interface B", family: "module-b", input: nil, output: pcm),
        AgentModuleEndpointCandidate(id: 4, name: "AS Interface A", family: "module-a", input: nil, output: pcm),
    ]
    let pair = AgentModuleEndpointSelector.resolve(candidates)
    expect(pair?.input.id == 2 && pair?.output.id == 4, "must skip invalid candidate and avoid cross-family pairing")
    expect(AgentModuleEndpointSelector.resolve(Array(candidates.prefix(3))) == nil, "must not mix unrelated module families")
    print("PASS module endpoint selection filters invalid formats and preserves family")
}

testPCM16FloatRoundTrip()
testFrameAccumulator()
testSupportedNativeFormats()
testEndpointPairingStaysWithinFamily()
