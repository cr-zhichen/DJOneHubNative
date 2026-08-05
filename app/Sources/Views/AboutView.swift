import SwiftUI
import AppKit
import Awesome

/// 关于与更新：系统设置风格的简洁分组表单
struct AboutView: View {
    @EnvironmentObject private var updateChecker: UpdateChecker

    var body: some View {
        Form {
            Section {
                HStack(spacing: 14) {
                    Image(nsImage: NSApplication.shared.applicationIconImage)
                        .resizable()
                        .frame(width: 56, height: 56)
                        .clipShape(RoundedRectangle(cornerRadius: 11, style: .continuous))
                    VStack(alignment: .leading, spacing: 2) {
                        Text("DJOneHub Native").font(.headline)
                        Text("版本 \(updateChecker.currentVersion)（构建 \(updateChecker.buildNumber)）")
                            .font(.callout).foregroundStyle(.secondary)
                        Text("构建于 \(buildDateText)")
                            .font(.caption).foregroundStyle(.tertiary)
                    }
                    Spacer()
                    Button {
                        if let url = URL(string: "https://github.com/cr-zhichen/DJOneHubNative") {
                            NSWorkspace.shared.open(url)
                        }
                    } label: {
                        Awesome.Brand.github.image
                            .size(26)
                            .foregroundColor(.primary)
                            .help("项目主页")
                    }
                    .buttonStyle(.plain)
                }
                .padding(.vertical, 2)
            }

            Section("更新检查") {
                Picker("更新渠道", selection: $updateChecker.channel) {
                    ForEach(UpdateChecker.Channel.allCases) { ch in
                        Text(ch.rawValue).tag(ch)
                    }
                }
                .pickerStyle(.segmented)
                .onChange(of: updateChecker.channel) { _ in
                    Task { await updateChecker.check() }
                }
                LabeledContent("当前版本", value: updateChecker.currentVersion)
                Button {
                    if updateChecker.hasNewerVersion {
                        updateChecker.unskipAndPrompt()
                    } else {
                        Task { await updateChecker.check(prompt: true) }
                    }
                } label: {
                    LabeledContent("最新版本") {
                        if updateChecker.checking {
                            ProgressView().controlSize(.small)
                        } else {
                            HStack(spacing: 4) {
                                Text(updateChecker.latestVersionString)
                                    .font(.callout.monospacedDigit())
                                if updateChecker.hasNewerVersion {
                                    Image(systemName: "arrow.up.right")
                                        .font(.caption)
                                }
                            }
                            .foregroundStyle(updateChecker.hasNewerVersion ? Color.accentColor : Color.primary)
                        }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(updateChecker.checking)
                .help(updateChecker.hasNewerVersion ? "点击查看更新" : "点击检查更新")
                LabeledContent("上次检查", value: updateChecker.lastCheckDate?.formatted(date: .abbreviated, time: .shortened) ?? "从未")
                statusRow
                Toggle("启动时自动检查更新", isOn: $updateChecker.autoCheck)
            }

            Section("许可证") {
                Text("PolyForm Noncommercial License 1.0.0（仅限非商业用途）")
                    .font(.callout)
                HStack(spacing: 14) {
                    licenseLink("LICENSE", path: "LICENSE")
                    licenseLink("第三方声明", path: "THIRD_PARTY_NOTICES.md")
                    licenseLink("上游项目", path: "https://github.com/ZenGeekLabs/DJOneHub")
                }
            }
        }
        .formStyle(.grouped)
        .frame(maxWidth: 520)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .navigationTitle("关于与更新")
    }

    // MARK: - 更新状态

    @ViewBuilder
    private var statusRow: some View {
        if let error = updateChecker.lastError {
            Text(error)
                .foregroundStyle(.red)
                .lineLimit(1)
        }
    }

    private func licenseLink(_ title: String, path: String) -> some View {
        Button(title) {
            if path.hasPrefix("http") {
                if let url = URL(string: path) { NSWorkspace.shared.open(url) }
            } else {
                let url = URL(string: "https://github.com/cr-zhichen/DJOneHubNative/blob/main/\(path)")!
                NSWorkspace.shared.open(url)
            }
        }
        .buttonStyle(.link)
        .controlSize(.small)
    }

    // MARK: - 构建时间

    private var buildDateText: String {
        guard let date = buildDate else { return "-" }
        return date.formatted(date: .abbreviated, time: .omitted)
    }

    private var buildDate: Date? {
        guard let path = Bundle.main.executablePath else { return nil }
        let attrs = try? FileManager.default.attributesOfItem(atPath: path)
        return attrs?[.modificationDate] as? Date
    }
}
