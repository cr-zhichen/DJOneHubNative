import Foundation
import AVFoundation
import Speech

/// macOS 27+ 原生实时转录适配器。
/// 输入固定为 QDC507 的 PCM16LE / 8000Hz / mono，不保存原始音频。
@available(macOS 27.0, *)
enum NativeSpeechTranscriberErrorPolicy {
    static func report(
        _ error: Error,
        onResult: (String, Bool) -> Void,
        onError: (String) -> Void
    ) {
        _ = onResult
        onError(error.localizedDescription)
    }
}

@available(macOS 27.0, *)
final class NativeSpeechTranscriber {
    struct Diagnostics: Sendable {
        let requestedLocale: String
        let supported: Bool
        let installed: Bool
        let assetStatus: String
        let inputFormat: String
        let compatibleFormats: [String]
    }

    enum TranscriberError: LocalizedError {
        case unsupportedLocale(String)
        case modelUnavailable(String)
        case invalidPCM
        case notStarted

        var errorDescription: String? {
            switch self {
            case .unsupportedLocale(let locale): return "系统不支持转录语言：\(locale)"
            case .modelUnavailable(let status): return "系统转录模型不可用：\(status)"
            case .invalidPCM: return "PCM 数据不是有效的单声道 16-bit 音频"
            case .notStarted: return "原生转录尚未启动"
            }
        }
    }

    typealias ResultHandler = @Sendable (_ text: String, _ isFinal: Bool) -> Void
    typealias ErrorHandler = @Sendable (_ message: String) -> Void

    private let locale: Locale
    private let onResult: ResultHandler
    private let onError: ErrorHandler
    private let inputFormat = AVAudioFormat(standardFormatWithSampleRate: 8_000, channels: 1)!
    private var continuation: AsyncStream<AnalyzerInput>.Continuation?
    private var converter: AnalyzerInputConverter?
    private var analyzer: SpeechAnalyzer?
    private var transcriber: SpeechTranscriber?
    private var task: Task<Void, Never>?
    private var started = false
    private let inputQueue = DispatchQueue(label: "com.djonehub.native-speech.input", qos: .userInitiated)

    init(
        localeIdentifier: String = "zh-CN",
        onResult: @escaping ResultHandler,
        onError: @escaping ErrorHandler = { _ in }
    ) {
        self.locale = Locale(identifier: localeIdentifier)
        self.onResult = onResult
        self.onError = onError
    }

    static func diagnostics(localeIdentifier: String = "zh-CN") async -> Diagnostics {
        let locale = Locale(identifier: localeIdentifier)
        let supportedLocale = await SpeechTranscriber.supportedLocale(equivalentTo: locale)
        let supported = supportedLocale != nil
        let installedLocales = await SpeechTranscriber.installedLocales
        let installed = installedLocales.contains { $0.identifier == supportedLocale?.identifier }
        let module = SpeechTranscriber(locale: supportedLocale ?? locale, preset: .progressiveTranscription)
        let status = await AssetInventory.status(forModules: [module])
        let formats = await module.availableCompatibleAudioFormats.map(Self.formatDescription)
        return Diagnostics(
            requestedLocale: localeIdentifier,
            supported: supported,
            installed: installed,
            assetStatus: String(describing: status),
            inputFormat: Self.formatDescription(AVAudioFormat(standardFormatWithSampleRate: 8_000, channels: 1)!),
            compatibleFormats: formats)
    }

    func start() async throws {
        guard !started else { return }
        guard let supportedLocale = await SpeechTranscriber.supportedLocale(equivalentTo: locale) else {
            throw TranscriberError.unsupportedLocale(locale.identifier)
        }

        let module = SpeechTranscriber(locale: supportedLocale, preset: .progressiveTranscription)
        let status = await AssetInventory.status(forModules: [module])
        guard status == .installed || status == .supported else {
            throw TranscriberError.modelUnavailable(String(describing: status))
        }

        let inputStream = AsyncStream<AnalyzerInput> { continuation in
            self.continuation = continuation
        }
        converter = try await AnalyzerInputConverter.converter(compatibleWith: [module])
        let speechAnalyzer = SpeechAnalyzer(modules: [module])
        try await speechAnalyzer.prepareToAnalyze(in: inputFormat)
        analyzer = speechAnalyzer
        transcriber = module
        started = true

        task = Task { [weak self, weak speechAnalyzer, weak module] in
            guard let self, let speechAnalyzer, let module else { return }
            do {
                try await speechAnalyzer.start(inputSequence: inputStream)
                for try await result in module.results {
                    self.onResult(String(result.text.characters), result.resultsFinalizationTime.isNumeric)
                }
            } catch is CancellationError {
                // 正常停止不作为错误上报。
            } catch {
                NativeSpeechTranscriberErrorPolicy.report(
                    error,
                    onResult: self.onResult,
                    onError: self.onError)
            }
        }
    }

    /// 线程安全入口：将一帧或多帧 PCM16LE 转成系统转录输入。
    func appendPCM16LE(_ data: Data) {
        inputQueue.async { [weak self] in
            self?.appendPCM16LEOnQueue(data)
        }
    }

    private func appendPCM16LEOnQueue(_ data: Data) {
        guard started, let converter else { return }
        guard data.count >= 2, data.count.isMultiple(of: 2) else { return }
        let sampleCount = data.count / 2
        guard let buffer = AVAudioPCMBuffer(pcmFormat: inputFormat, frameCapacity: AVAudioFrameCount(sampleCount)),
              let channel = buffer.floatChannelData?[0] else { return }
        buffer.frameLength = AVAudioFrameCount(sampleCount)
        data.withUnsafeBytes { raw in
            for index in 0..<sampleCount {
                let value = raw.loadUnaligned(fromByteOffset: index * 2, as: Int16.self)
                channel[index] = Float32(Int16(littleEndian: value)) / 32768.0
            }
        }
        do {
            for input in try converter.convert(buffer, at: nil) {
                continuation?.yield(input)
            }
        } catch {
            NativeSpeechTranscriberErrorPolicy.report(
                error,
                onResult: onResult,
                onError: onError)
        }
    }

    func stop() async {
        guard started else { return }
        continuation?.finish()
        continuation = nil
        try? await analyzer?.finalizeAndFinishThroughEndOfInput()
        task?.cancel()
        task = nil
        analyzer = nil
        transcriber = nil
        converter = nil
        started = false
    }

    private static func formatDescription(_ format: AVAudioFormat) -> String {
        "\(format.sampleRate)Hz/\(format.channelCount)ch/\(format.commonFormat.rawValue)"
    }
}
