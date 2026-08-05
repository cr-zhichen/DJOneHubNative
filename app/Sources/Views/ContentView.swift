import SwiftUI

enum AppSection: String, CaseIterable, Identifiable {
    case home = "首页"
    case sms = "短信"
    case esim = "eSIM 卡片"
    case debug = "调试与诊断"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .home: return "house.fill"
        case .sms: return "message.fill"
        case .esim: return "simcard.fill"
        case .debug: return "terminal.fill"
        }
    }
}

struct ContentView: View {
    @EnvironmentObject private var backend: BackendProcess
    @State private var selection: AppSection? = .home

    var body: some View {
        NavigationSplitView {
            List(AppSection.allCases, selection: $selection) { section in
                Label(section.rawValue, systemImage: section.icon)
                    .tag(section)
            }
            .navigationSplitViewColumnWidth(min: 150, ideal: 170)
            .navigationTitle("DJOneHub")
        } detail: {
            switch selection ?? .home {
            case .home:
                HomeView()
            default:
                if backend.state == .running {
                    sectionView
                } else {
                    ServiceOffView()
                }
            }
        }
    }

    @ViewBuilder
    private var sectionView: some View {
        switch selection ?? .home {
        case .home: HomeView()
        case .sms: SMSView()
        case .esim: ESIMView()
        case .debug: DiagnosticsView()
        }
    }
}

/// 服务未运行时占位页
struct ServiceOffView: View {
    @EnvironmentObject private var backend: BackendProcess

    var body: some View {
        VStack(spacing: 14) {
            Image(systemName: "power")
                .font(.system(size: 44))
                .foregroundStyle(.tertiary)
            Text("服务未运行")
                .font(.title3)
            if case .failed(let reason) = backend.state, !reason.isEmpty {
                Text(reason)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 400)
            } else {
                Text("服务启动异常，请稍后重试。")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Button("重试") {
                backend.start()
            }
            .buttonStyle(.borderedProminent)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
