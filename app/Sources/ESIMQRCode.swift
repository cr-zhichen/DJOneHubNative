import AppKit
import AVFoundation
import CoreMedia
import SwiftUI
import Vision

enum ESIMQRCodeError: LocalizedError {
    case noQRCode
    case notActivationCode
    case recognitionFailed(String)

    var errorDescription: String? {
        switch self {
        case .noQRCode:
            return "图片中没有识别到二维码"
        case .notActivationCode:
            return "识别到了二维码，但内容不是 eSIM 激活码"
        case .recognitionFailed(let message):
            return "二维码识别失败：\(message)"
        }
    }
}

enum ESIMQRCodeDecoder {
    static func activationCode(in imageData: Data) throws -> String {
        let request = VNDetectBarcodesRequest()
        request.symbologies = [.qr]

        do {
            try VNImageRequestHandler(data: imageData).perform([request])
        } catch {
            throw ESIMQRCodeError.recognitionFailed(error.localizedDescription)
        }

        let payloads = request.results?.compactMap(\.payloadStringValue) ?? []
        if let activationCode = payloads.compactMap(activationCode(from:)).first {
            return activationCode
        }
        throw payloads.isEmpty ? ESIMQRCodeError.noQRCode : ESIMQRCodeError.notActivationCode
    }

    static func activationCode(from rawValue: String) -> String? {
        let value = rawValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard value.uppercased().hasPrefix("LPA:") else { return nil }
        return value
    }
}

struct ESIMQRCodeScannerSheet: View {
    @Environment(\.dismiss) private var dismiss
    @StateObject private var scanner = ESIMQRCodeCameraScanner()

    let onActivationCode: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("扫描 eSIM 二维码")
                .font(.headline)

            ZStack {
                Color.black
                ESIMCameraPreview(session: scanner.session)
                    .opacity(showsCameraPreview ? 1 : 0)

                if showsScanFrame {
                    RoundedRectangle(cornerRadius: 16)
                        .stroke(Color.white.opacity(0.82), lineWidth: 2)
                        .frame(width: 220, height: 220)
                        .accessibilityHidden(true)
                }

                if !showsCameraPreview {
                    scannerStateOverlay
                }

                if showsCameraPreview || scanner.state == .preparing {
                    VStack {
                        Spacer()
                        Text(scannerStatusText)
                            .font(.caption.weight(.medium))
                            .foregroundStyle(.white)
                            .multilineTextAlignment(.center)
                            .padding(.horizontal, 12)
                            .padding(.vertical, 7)
                            .background(Color.black.opacity(0.62), in: Capsule())
                            .padding(14)
                    }
                }
            }
            .frame(width: 520, height: 340)
            .clipShape(RoundedRectangle(cornerRadius: 12))
            .overlay(
                RoundedRectangle(cornerRadius: 12)
                    .stroke(Color(nsColor: .separatorColor), lineWidth: 1)
            )

            HStack(alignment: .center, spacing: 12) {
                Label("画面仅在本机识别，不会保存或上传", systemImage: "lock.shield")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer(minLength: 12)
                Button("取消") { dismiss() }
                    .keyboardShortcut(.cancelAction)
            }
        }
        .padding(20)
        .frame(width: 560)
        .onAppear { scanner.start() }
        .onDisappear { scanner.stop() }
        .onChange(of: scanner.detectedActivationCode) { value in
            guard let value else { return }
            onActivationCode(value)
            dismiss()
        }
    }

    private var showsCameraPreview: Bool {
        switch scanner.state {
        case .scanning, .invalidCode:
            return true
        default:
            return false
        }
    }

    private var showsScanFrame: Bool {
        switch scanner.state {
        case .scanning, .invalidCode:
            return true
        default:
            return false
        }
    }

    private var scannerStatusText: String {
        switch scanner.state {
        case .preparing:
            return "正在准备摄像头…"
        case .scanning:
            return "将运营商提供的二维码置于框内"
        case .invalidCode:
            return "已识别二维码，但它不是 eSIM 激活码"
        case .permissionDenied:
            return "请在系统设置的隐私与安全性中允许摄像头访问"
        case .unavailable:
            return "没有检测到可用摄像头"
        case .failed:
            return "摄像头暂时不可用"
        }
    }

    @ViewBuilder
    private var scannerStateOverlay: some View {
        switch scanner.state {
        case .preparing:
            ProgressView()
                .controlSize(.small)
                .tint(.white)
        case .permissionDenied:
            scannerUnavailableView(
                systemImage: "camera.fill",
                title: "未获得摄像头权限",
                detail: "请在系统设置 → 隐私与安全性 → 摄像头中允许 DJOneHub。")
        case .unavailable:
            scannerUnavailableView(
                systemImage: "camera.slash.fill",
                title: "没有可用摄像头",
                detail: "连接摄像头后关闭并重新打开此窗口。")
        case .failed(let message):
            scannerUnavailableView(
                systemImage: "exclamationmark.triangle.fill",
                title: "无法启动摄像头",
                detail: message)
        case .scanning, .invalidCode:
            EmptyView()
        }
    }

    private func scannerUnavailableView(systemImage: String, title: String, detail: String) -> some View {
        VStack(spacing: 8) {
            Image(systemName: systemImage)
                .font(.largeTitle)
                .foregroundStyle(.white.opacity(0.78))
            Text(title)
                .font(.headline)
                .foregroundStyle(.white)
            Text(detail)
                .font(.caption)
                .foregroundStyle(.white.opacity(0.72))
                .multilineTextAlignment(.center)
                .frame(maxWidth: 320)
        }
        .padding(20)
    }
}

private final class ESIMQRCodeCameraScanner: NSObject, ObservableObject, AVCaptureVideoDataOutputSampleBufferDelegate {
    enum State: Equatable {
        case preparing
        case scanning
        case invalidCode
        case permissionDenied
        case unavailable
        case failed(String)
    }

    @Published private(set) var state: State = .preparing
    @Published private(set) var detectedActivationCode: String?

    let session = AVCaptureSession()

    private let sessionQueue = DispatchQueue(label: "com.djonehub.esim.qr-camera.session")
    private let frameQueue = DispatchQueue(label: "com.djonehub.esim.qr-camera.frames")
    private let minimumScanInterval: TimeInterval = 0.2
    private var isConfigured = false
    private var hasDeliveredCode = false
    private var isShowingInvalidCode = false
    private var lastScanTime: TimeInterval = 0
    private var consecutiveVisionFailures = 0
    private lazy var barcodeRequest: VNDetectBarcodesRequest = {
        let request = VNDetectBarcodesRequest()
        request.symbologies = [.qr]
        return request
    }()

    func start() {
        hasDeliveredCode = false
        isShowingInvalidCode = false
        lastScanTime = 0
        consecutiveVisionFailures = 0
        detectedActivationCode = nil
        state = .preparing

        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized:
            configureAndStart()
        case .notDetermined:
            AVCaptureDevice.requestAccess(for: .video) { [weak self] granted in
                if granted {
                    self?.configureAndStart()
                } else {
                    self?.publish(.permissionDenied)
                }
            }
        case .denied, .restricted:
            state = .permissionDenied
        @unknown default:
            state = .permissionDenied
        }
    }

    func stop() {
        sessionQueue.async { [weak self] in
            guard let self, self.session.isRunning else { return }
            self.session.stopRunning()
        }
    }

    private func configureAndStart() {
        sessionQueue.async { [weak self] in
            guard let self else { return }
            do {
                if !self.isConfigured {
                    try self.configureSession()
                    self.isConfigured = true
                }
                if !self.session.isRunning {
                    self.session.startRunning()
                }
                self.publish(.scanning)
            } catch CameraSetupError.noCamera {
                self.publish(.unavailable)
            } catch {
                self.publish(.failed(error.localizedDescription))
            }
        }
    }

    private func configureSession() throws {
        session.beginConfiguration()
        defer { session.commitConfiguration() }
        session.sessionPreset = session.canSetSessionPreset(.hd1280x720) ? .hd1280x720 : .high

        guard let camera = preferredCamera() else {
            throw CameraSetupError.noCamera
        }
        let input = try AVCaptureDeviceInput(device: camera)
        guard session.canAddInput(input) else {
            throw CameraSetupError.cannotAddInput
        }
        session.addInput(input)

        let output = AVCaptureVideoDataOutput()
        output.alwaysDiscardsLateVideoFrames = true
        guard session.canAddOutput(output) else {
            throw CameraSetupError.cannotAddOutput
        }
        session.addOutput(output)
        output.setSampleBufferDelegate(self, queue: frameQueue)
    }

    private func preferredCamera() -> AVCaptureDevice? {
        let builtInCamera = AVCaptureDevice.DiscoverySession(
            deviceTypes: [.builtInWideAngleCamera],
            mediaType: .video,
            position: .unspecified
        ).devices.first
        return builtInCamera ?? AVCaptureDevice.default(for: .video)
    }

    func captureOutput(
        _ output: AVCaptureOutput,
        didOutput sampleBuffer: CMSampleBuffer,
        from connection: AVCaptureConnection
    ) {
        guard !hasDeliveredCode else { return }
        let now = ProcessInfo.processInfo.systemUptime
        guard now - lastScanTime >= minimumScanInterval else { return }
        lastScanTime = now
        guard let pixelBuffer = CMSampleBufferGetImageBuffer(sampleBuffer) else { return }

        do {
            let handler = VNImageRequestHandler(cvPixelBuffer: pixelBuffer, orientation: .up)
            try handler.perform([barcodeRequest])
            consecutiveVisionFailures = 0
        } catch {
            consecutiveVisionFailures += 1
            guard consecutiveVisionFailures >= 3 else { return }
            hasDeliveredCode = true
            stop()
            publish(.failed("实时二维码识别失败：\(error.localizedDescription)"))
            return
        }

        let payloads = barcodeRequest.results?.compactMap(\.payloadStringValue) ?? []
        guard !payloads.isEmpty else { return }
        guard let activationCode = payloads.compactMap(ESIMQRCodeDecoder.activationCode(from:)).first else {
            if !isShowingInvalidCode {
                isShowingInvalidCode = true
                publish(.invalidCode)
            }
            return
        }

        hasDeliveredCode = true
        stop()
        DispatchQueue.main.async { [weak self] in
            self?.detectedActivationCode = activationCode
        }
    }

    private func publish(_ newState: State) {
        DispatchQueue.main.async { [weak self] in
            self?.state = newState
        }
    }
}

private enum CameraSetupError: LocalizedError {
    case noCamera
    case cannotAddInput
    case cannotAddOutput

    var errorDescription: String? {
        switch self {
        case .noCamera:
            return "没有检测到可用摄像头"
        case .cannotAddInput:
            return "无法连接摄像头输入"
        case .cannotAddOutput:
            return "无法读取摄像头画面"
        }
    }
}

private struct ESIMCameraPreview: NSViewRepresentable {
    let session: AVCaptureSession

    func makeNSView(context: Context) -> CameraPreviewView {
        let view = CameraPreviewView()
        view.previewLayer.session = session
        return view
    }

    func updateNSView(_ nsView: CameraPreviewView, context: Context) {
        nsView.previewLayer.session = session
    }

    static func dismantleNSView(_ nsView: CameraPreviewView, coordinator: ()) {
        nsView.previewLayer.session = nil
    }
}

private final class CameraPreviewView: NSView {
    let previewLayer = AVCaptureVideoPreviewLayer()

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        wantsLayer = true
        previewLayer.videoGravity = .resizeAspectFill
        layer?.addSublayer(previewLayer)
    }

    required init?(coder: NSCoder) {
        return nil
    }

    override func layout() {
        super.layout()
        previewLayer.frame = bounds
    }
}
