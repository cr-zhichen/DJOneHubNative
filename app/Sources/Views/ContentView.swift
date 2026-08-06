import SwiftUI

enum AppSection: String, CaseIterable, Identifiable {
    case home = "首页"
    case sms = "短信"
    case esim = "eSIM 卡片"
    case routing = "应用分流"
    case debug = "调试与诊断"
    case about = "关于与更新"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .home: return "house.fill"
        case .sms: return "message.fill"
        case .esim: return "simcard.fill"
        case .routing: return "point.3.connected.trianglepath.dotted"
        case .debug: return "terminal.fill"
        case .about: return "info.circle"
        }
    }
}

struct ContentView: View {
    @EnvironmentObject private var backend: BackendProcess
    @EnvironmentObject private var store: DashboardStore
    @EnvironmentObject private var smsStore: SMSStore
    @EnvironmentObject private var updateChecker: UpdateChecker
    @State private var selection: AppSection? = .home
    @State private var showUpdatePrompt = false

    var body: some View {
        NavigationSplitView {
            List(selection: $selection) {
                ForEach(AppSection.allCases) { section in
                    Label(section.rawValue, systemImage: section.icon)
                        .tag(section)
                }
            }
            .listStyle(.sidebar)
            .navigationSplitViewColumnWidth(min: 150, ideal: 170)
            .navigationTitle("DJOneHub")
        } detail: {
            switch selection ?? .home {
            case .home:
                HomeView()
            case .about:
                AboutView()
            default:
                if backend.state == .running {
                    sectionView
                } else {
                    ServiceOffView()
                }
            }
        }
        .onAppear {
            smsStore.viewingSMS = selection == .sms
            showUpdatePrompt = updateChecker.pendingUpdate != nil
            // 启动即点击通知时，pendingOpenSender 可能早于 onChange 挂载设置，这里补一次
            if smsStore.pendingOpenSender != nil {
                selection = .sms
            }
        }
        .onChange(of: selection) { newValue in
            smsStore.viewingSMS = newValue == .sms
        }
        .onChange(of: updateChecker.pendingUpdate != nil) { shown in
            showUpdatePrompt = shown
        }
        .alert(
            "发现新版本",
            isPresented: $showUpdatePrompt,
            presenting: updateChecker.pendingUpdate
        ) { release in
            Button("前往更新") {
                if let url = updateChecker.downloadURL {
                    NSWorkspace.shared.open(url)
                }
                updateChecker.dismissUpdate()
            }
            .keyboardShortcut(.defaultAction)
            Button("暂不更新", role: .cancel) {
                updateChecker.dismissUpdate()
            }
            Button("跳过本次更新") {
                updateChecker.skipUpdate(release)
            }
        } message: { release in
            Text("当前版本 \(updateChecker.currentVersion)，新版本 \(release.tagName) 已发布。")
        }
        .onChange(of: smsStore.pendingOpenSender) { sender in
            // 点击短信通知：切到短信页（号码选择由短信页处理）
            if sender != nil {
                selection = .sms
            }
        }
        // 来电详情弹窗（根层挂载，任意页面都能弹出）
        .sheet(isPresented: $store.showCallDetail) {
            CallDetailView()
        }
        .onChange(of: store.showCallDetail) { shown in
            // 点击来电通知：切回首页并弹出通话详情
            if shown {
                selection = .home
            }
        }
    }

    @ViewBuilder
    private var sectionView: some View {
        switch selection ?? .home {
        case .home: HomeView()
        case .sms: SMSView()
        case .esim: ESIMView()
        case .routing: TrafficRoutingView()
        case .debug: DiagnosticsView()
        case .about: AboutView()
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
