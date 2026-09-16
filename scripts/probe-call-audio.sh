#!/bin/bash
# 离线验证真实音频核心：不启动应用/模块，不申请麦克风，也不修改音频偏好。
set -euo pipefail
cd "$(dirname "$0")/.."
probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/djonehub-audio-probe.XXXXXX")
trap 'rm -rf "$probe_dir"' EXIT
cat app/Sources/AudioBridge.swift > "$probe_dir/main.swift"
cat >> "$probe_dir/main.swift" <<'SWIFT'

extension AudioPipeline {
    static func probePCM() {
        let pcm = DeviceFormat(sampleRate: 8_000, channels: 1)
        let pipeline = AudioPipeline(moduleInput: pcm, moduleOutput: pcm, voice: pcm)
        for bits: UInt32 in [16, 32] {
            let format = DeviceFormat(sampleRate: 8_000, channels: 1, bits: bits)
            var encoded = Data()
            for sample: Float32 in [-2, -1, 0, 1, 2, .nan, .infinity, -.infinity] {
                pipeline.appendSample(sample, format: format, to: &encoded)
            }
            let bytes = Int(bits / 8)
            let values: [Int64] = encoded.withUnsafeBytes { raw in
                stride(from: 0, to: raw.count, by: bytes).map { offset in
                    bits == 16 ? Int64(raw.loadUnaligned(fromByteOffset: offset, as: Int16.self))
                               : Int64(raw.loadUnaligned(fromByteOffset: offset, as: Int32.self))
                }
            }
            let maximum = bits == 16 ? Int64(Int16.max) : Int64(Int32.max)
            precondition(values == [-maximum, -maximum, 0, maximum, maximum, 0, 0, 0],
                         "PCM 满幅、饱和或非有限样本转换错误：\(bits) bit")
        }

        // 输出设备可能使用一个交错双声道 AudioBuffer；每一帧都必须复制到左右声道。
        var samples: [Float32] = [1, -1]
        var output = [Int16](repeating: 123, count: 8)
        output.withUnsafeMutableBytes { bytes in
            var list = AudioBufferList(mNumberBuffers: 1, mBuffers: AudioBuffer(
                mNumberChannels: 2, mDataByteSize: UInt32(bytes.count), mData: bytes.baseAddress))
            pipeline.writeSamples(&samples, format: DeviceFormat(sampleRate: 8_000, channels: 2), into: &list)
        }
        precondition(output == [32767, 32767, -32767, -32767, 0, 0, 0, 0], "交错输出或欠载静音错误")
        precondition(samples.isEmpty, "已播放样本未消费")
        print("PASS PCM: Int16/Int32 满幅、饱和、NaN/Inf、双声道输出与欠载静音")
    }
}

func probeResampler() {
    // 同一输入逐样本/不规则分块应与整块结果一致，覆盖降采样跨块越界后的相位。
    let input = (0..<9_601).map { Float32(sin(Double($0) * 0.03)) }
    for rates in [(48_000.0, 8_000.0), (8_000.0, 48_000.0), (44_100.0, 8_000.0)] {
        let whole = FloatResampler(inRate: rates.0, outRate: rates.1).process(input)
        let chunked = FloatResampler(inRate: rates.0, outRate: rates.1)
        var output: [Float32] = []
        var offset = 0
        let chunks = [1, 2, 7, 113, 1, 512, 3]
        var index = 0
        while offset < input.count {
            let end = min(input.count, offset + chunks[index % chunks.count])
            output.append(contentsOf: chunked.process(Array(input[offset..<end])))
            offset = end
            index += 1
        }
        precondition(abs(output.count - whole.count) <= 1, "分块后采样数量漂移")
        precondition(zip(output, whole).allSatisfy { abs($0 - $1) < 0.000_01 }, "分块后重采样相位漂移")
    }
    print("PASS resampler: 48k/8k 双向及 44.1k→8k 不规则分块连续性")
}

extension AudioBridge {
    func prepareStopProbe() {
        controlQueue.async {
            // 模拟排队中的音频生命周期操作，不创建引擎或访问麦克风。
            Thread.sleep(forTimeInterval: 0.1)
            self.wantsAudio = true
            self.state.phase = .connecting
            let work = DispatchWorkItem { fatalError("stop 未取消待执行的重建") }
            self.reconfigureWork = work
            self.controlQueue.asyncAfter(deadline: .now() + .milliseconds(200), execute: work)
        }
    }

    func verifyStoppedForProbe() async {
        await withCheckedContinuation { continuation in
            controlQueue.async {
                precondition(!self.wantsAudio && self.engine == nil && self.registrations.isEmpty,
                             "stop 返回时仍有活跃管线")
                precondition(self.reconfigureWork == nil && self.state.phase == .idle,
                             "stop 返回时仍有待执行的重建")
                continuation.resume()
            }
        }
    }
}

AudioPipeline.probePCM()
probeResampler()
let bridge = AudioBridge.shared
var receivedCatalog = false
bridge.observe { state in
    guard !receivedCatalog else { return }
    receivedCatalog = true
    print("PASS device catalog: 只读枚举输入 \(state.inputs.count) 个、输出 \(state.outputs.count) 个；未申请麦克风或改动默认设备")
    Task {
        let started = Date()
        bridge.prepareStopProbe()
        await bridge.stop()
        precondition(Date().timeIntervalSince(started) >= 0.08, "stop 未等待控制队列中的生命周期操作")
        await bridge.verifyStoppedForProbe()
        try? await Task.sleep(nanoseconds: 350_000_000)
        print("PASS stop: 等待主机清理完成，取消过期待执行重建")
        exit(0)
    }
}
DispatchQueue.main.asyncAfter(deadline: .now() + .seconds(10)) {
    fatalError("音频核心验证超时")
}
RunLoop.main.run()
SWIFT
xcrun swiftc -swift-version 5 -target "$(uname -m)-apple-macos13.0" "$probe_dir/main.swift" -o "$probe_dir/probe"
"$probe_dir/probe"
