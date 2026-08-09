import AppKit
import SwiftUI
import UniformTypeIdentifiers

struct ESIMView: View {
    @Environment(\.accessibilityReduceMotion) private var accessibilityReduceMotion
    @StateObject private var store = ESIMStore()

    @State private var showDownload = false
    @State private var editingProfile: ESIMProfileContext?
    @State private var renamingProfile: ESIMProfileContext?
    @State private var deletingProfile: ESIMProfileContext?
    @State private var enablingProfile: ESIMProfileContext?
    @State private var isChipInfoExpanded = false
    @State private var isChipHeaderHovered = false

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            if let banner = store.banner {
                ESIMStatusBanner(banner: banner, onDismiss: store.dismissBanner)
                    .padding(.horizontal, 18)
                    .padding(.bottom, 10)
            } else if let operation = store.operation, operation.isActive {
                ESIMOperationBanner(operation: operation)
                    .padding(.horizontal, 18)
                    .padding(.bottom, 10)
            }
            Divider()
            content
        }
        .navigationTitle("eSIM 卡片")
        .onAppear { store.load() }
        .sheet(isPresented: $showDownload) {
            DownloadESIMView(store: store)
        }
        .sheet(item: $renamingProfile) { context in
            RenameProfileView(context: context, onDone: store.reloadAfterMetadataChange)
        }
        .sheet(item: $editingProfile) { context in
            ProfileNoteView(
                context: context,
                localNote: store.notes[context.profile.iccid],
                moduleNote: store.moduleNotes?.notes?[context.profile.iccid],
                moduleStatus: store.moduleNotes.map { "\($0.used ?? 0)/\($0.total ?? 0)" },
                moduleUnavailableReason: store.moduleNotesError,
                onDone: store.reloadAfterMetadataChange)
        }
        .confirmationDialog("启用这个 Profile？", isPresented: Binding(
            get: { enablingProfile != nil },
            set: { if !$0 { enablingProfile = nil } }
        ), titleVisibility: .visible) {
            Button("启用 \(enablingProfile?.profile.displayName ?? "Profile")") {
                guard let context = enablingProfile else { return }
                enablingProfile = nil
                store.switchProfile(context)
            }
            Button("取消", role: .cancel) {}
        } message: {
            Text("切换期间蜂窝网络会暂时断开，模块将自动重启并重新注册网络。")
        }
        .confirmationDialog("删除 Profile？", isPresented: Binding(
            get: { deletingProfile != nil },
            set: { if !$0 { deletingProfile = nil } }
        ), titleVisibility: .visible) {
            Button("删除 \(deletingProfile?.profile.displayName ?? "Profile")", role: .destructive) {
                guard let context = deletingProfile else { return }
                deletingProfile = nil
                store.deleteProfile(context)
            }
            Button("取消", role: .cancel) {}
        } message: {
            Text("删除 Profile 通常不可撤销。写入过程中不要拔出模块。")
        }
    }

    private var toolbar: some View {
        HStack(spacing: 10) {
            toolbarTitle
            healthLabelCompact
            Spacer(minLength: 12)
            refreshButton(compact: true)
            moreMenu(compact: true)
            downloadButton(compact: true)
        }
        .padding(.horizontal, 18)
        .padding(.vertical, 10)
    }

    private var toolbarTitle: some View {
        HStack(spacing: 8) {
            Text("Profile 管理").font(.headline)
            Text(toolbarCardType)
                .font(.caption)
                .frame(width: 52)
                .padding(.horizontal, 6)
                .padding(.vertical, 3)
                .background(Capsule().fill(Color.secondary.opacity(0.12)))
                .foregroundStyle(store.overview == nil ? .secondary : .primary)
        }
        .fixedSize()
    }

    private var toolbarCardType: String {
        guard let overview = store.overview else { return "读取中" }
        return overview.cardType == "physical_sim" ? "实体 SIM" : "eUICC"
    }

    @ViewBuilder
    private var healthLabelCompact: some View {
        if store.health?.state == "ready" {
            HoverStatusIndicator(
                systemImage: "checkmark.circle.fill",
                color: .green,
                title: "网络已就绪",
                message: "当前启用的 Profile 与模块状态一致，蜂窝网络已注册。")
        } else if let message = store.health?.message, !message.isEmpty {
            HoverStatusIndicator(
                systemImage: "info.circle.fill",
                color: .orange,
                title: "eSIM 状态提醒",
                message: message)
        } else if let error = store.healthError {
            HoverStatusIndicator(
                systemImage: "exclamationmark.triangle.fill",
                color: .orange,
                title: "网络状态暂不可用",
                message: error)
        } else {
            ProgressView()
                .controlSize(.mini)
                .frame(width: 20, height: 20)
                .help(store.overview == nil ? "正在读取 eSIM 卡片信息" : "正在检查 eSIM 状态")
                .accessibilityLabel(store.overview == nil ? "正在读取 eSIM 卡片信息" : "正在检查 eSIM 状态")
        }
    }

    private func refreshButton(compact: Bool) -> some View {
        Button {
            store.refresh()
        } label: {
            if store.isRefreshing {
                ProgressView().controlSize(.small)
            } else if compact {
                Image(systemName: "arrow.clockwise")
            } else {
                Label("刷新", systemImage: "arrow.clockwise")
            }
        }
        .disabled(store.isBusy || store.capabilities?.canRefresh != true)
        .help(refreshHelp)
        .accessibilityLabel("刷新卡片信息")
    }

    private var refreshHelp: String {
        guard let updatedAt = store.overview?.updatedAt else {
            return "重新读取卡片和 Profile 信息"
        }
        return "重新读取卡片和 Profile 信息；上次更新于 \(updatedAt.formatted(date: .omitted, time: .standard))"
    }

    private func moreMenu(compact: Bool) -> some View {
        Menu {
            Button {
                store.probePhonebook()
            } label: {
                Label("模块资料库检测", systemImage: "book.closed")
            }
            .disabled(store.isBusy || store.capabilities?.canProbePhonebook != true)
        } label: {
            if compact {
                Image(systemName: "ellipsis.circle")
            } else {
                Label("更多", systemImage: "ellipsis.circle")
            }
        }
        .help("更多 eSIM 操作")
        .accessibilityLabel("更多 eSIM 操作")
    }

    private func downloadButton(compact: Bool) -> some View {
        Button {
            showDownload = true
        } label: {
            Label(compact ? "下载" : "下载 Profile", systemImage: "arrow.down.circle")
        }
        .buttonStyle(.borderedProminent)
        .disabled(store.isBusy || store.capabilities?.canDownload != true)
    }

    @ViewBuilder
    private var content: some View {
        if case .failed(let message) = store.loadPhase, store.overview == nil {
            loadFailure(message)
        } else {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    if let overview = store.overview {
                        if let message = overview.message, !message.isEmpty {
                            Text(message).font(.callout).foregroundStyle(.secondary)
                        }
                        chipSection(overview)
                        if store.isPhysicalSIM {
                            physicalSIMEmptyState
                        } else if store.profileCount > 0 || hasProfileReadError {
                            profilesSection
                        } else {
                            emptyProfiles
                        }
                    } else {
                        chipLoadingSection
                        loadingProfiles
                    }
                }
                .padding(18)
            }
            .scrollIndicators(.visible)
        }
    }

    private func chipSection(_ overview: ESIMOverview) -> some View {
        let chip = overview.chipInfo
        let eids = chip?.eids ?? []
        return VStack(spacing: 0) {
            Button(action: toggleChipInfo) {
                chipHeader(chip, eids: eids)
                    .padding(14)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .contentShape(Rectangle())
            }
            .buttonStyle(ChipDisclosureButtonStyle(
                isHovered: isChipHeaderHovered,
                reduceMotion: accessibilityReduceMotion))
            .onHover { isChipHeaderHovered = $0 }
            .accessibilityLabel("卡片信息，\(chipModelSummary(chip))")
            .accessibilityValue(isChipInfoExpanded ? "已展开" : "已收起")
            .accessibilityHint(isChipInfoExpanded ? "按下以收起卡片详情" : "按下以展开卡片详情")

            if isChipInfoExpanded {
                chipDetails(chip, eids: eids)
                    .padding(.horizontal, 14)
                    .padding(.bottom, 14)
                    .transition(.opacity)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }

    private var chipLoadingSection: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 8) {
                Text("卡片信息")
                    .font(.headline)
                Spacer(minLength: 12)
                ProgressView()
                    .controlSize(.mini)
                Text("读取中")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .frame(width: 12)
                    .hidden()
            }
            Text("正在识别型号、固件与 EID…")
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color(nsColor: .separatorColor), lineWidth: 1))
        .accessibilityElement(children: .combine)
        .accessibilityLabel("正在读取卡片信息")
    }

    private var loadingProfiles: some View {
        VStack(spacing: 8) {
            ProgressView()
                .controlSize(.small)
            Text("正在读取 Profile…")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 32)
        .accessibilityElement(children: .combine)
    }

    private func chipHeader(_ chip: ChipInfo?, eids: [EUICCInfo]) -> some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 8) {
                Text("卡片信息")
                    .font(.headline)
                Spacer(minLength: 12)
                Text(chipModelSummary(chip))
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.tertiary)
                    .frame(width: 12)
                    .rotationEffect(.degrees(isChipInfoExpanded ? 90 : 0))
                    .accessibilityHidden(true)
            }
            chipSummaryLine(chip, eids: eids)
        }
    }

    @ViewBuilder
    private func chipDetails(_ chip: ChipInfo?, eids: [EUICCInfo]) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Divider()
            Grid(alignment: .leading, horizontalSpacing: 20, verticalSpacing: 6) {
                GridRow { keyText("型号"); valueText(chip?.identity?.model ?? chip?.skuName ?? "未识别") }
                if let revision = chip?.identity?.hardwareRevision, !revision.isEmpty {
                    GridRow { keyText("硬件修订"); valueText(revision) }
                }
                GridRow { keyText("厂商序列号"); valueText(chip?.serialNumber ?? "卡片未提供") }
                GridRow { keyText("固件"); valueText(chip?.firmware ?? "未提供") }
                if let identity = chip?.identity {
                    GridRow { keyText("识别依据"); valueText(identityDescription(identity)) }
                    if let evidence = identity.evidence, !evidence.isEmpty {
                        ForEach(Array(evidence.enumerated()), id: \.offset) { _, item in
                            if let url = URL(string: item.url) {
                                GridRow {
                                    keyText(item.kind == "eid_family" ? "EID 来源" : "固件规则")
                                    Link(identityEvidenceLabel(item), destination: url)
                                }
                            }
                        }
                    } else if let sourceURL = identity.sourceURL, let url = URL(string: sourceURL) {
                        GridRow {
                            keyText("规则来源")
                            Link(identitySourceLabel(identity), destination: url)
                        }
                    }
                }
            }
            .font(.callout)

            ForEach(Array(eids.enumerated()), id: \.offset) { index, info in
                if eids.count > 1 {
                    Divider().padding(.vertical, 2)
                    Text("eUICC \(index + 1)")
                        .font(.subheadline.weight(.semibold))
                }
                Grid(alignment: .leading, horizontalSpacing: 20, verticalSpacing: 6) {
                    GridRow { keyText("制造商"); valueText(info.manufacturer ?? "未识别") }
                    GridRow { keyText("EID"); valueText(info.eid ?? "未提供") }
                    GridRow { keyText("可用空间"); valueText(info.freeNvram ?? "未提供") }
                    if let source = info.infoSource, !source.isEmpty {
                        GridRow { keyText("信息来源"); valueText(source == "euicc_info2" ? "GSMA EUICCInfo2" : "GSMA EUICCInfo1") }
                    }
                }
                .font(.callout)
                if let error = info.infoError, !error.isEmpty {
                    Label("部分扩展信息读取失败，可尝试刷新", systemImage: "exclamationmark.triangle")
                        .font(.caption)
                        .help(error)
                }
            }
        }
    }

    private func toggleChipInfo() {
        let animation: Animation? = accessibilityReduceMotion
            ? nil
            : .interactiveSpring(response: 0.28, dampingFraction: 1, blendDuration: 0)
        withAnimation(animation) {
            isChipInfoExpanded.toggle()
        }
    }

    private func chipModelSummary(_ chip: ChipInfo?) -> String {
        let model = chip?.identity?.model ?? chip?.skuName ?? "未识别"
        guard let revision = chip?.identity?.hardwareRevision, !revision.isEmpty else { return model }
        return "\(model) · \(revision)"
    }

    private func chipSummaryLine(_ chip: ChipInfo?, eids: [EUICCInfo]) -> some View {
        let first = eids.first
        let eidSummary: String
        if eids.count > 1 {
            eidSummary = "\(eids.count) 个 eUICC"
        } else if let eid = first?.eid, !eid.isEmpty {
            eidSummary = "…\(eid.suffix(8))"
        } else {
            eidSummary = "未提供"
        }
        return HStack(spacing: 8) {
            Text("固件 \(chip?.firmware ?? "未提供")")
            Text("·")
                .accessibilityHidden(true)
            Text("EID \(eidSummary)")
                .help(first?.eid ?? "EID 未提供")
            Text("·")
                .accessibilityHidden(true)
            Text("可用 \(first?.freeNvram ?? "未提供")")
            Spacer(minLength: 0)
        }
        .font(.caption)
        .foregroundStyle(.secondary)
        .lineLimit(1)
    }

    private var profilesSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("已安装 Profile（\(store.profileCount)）").font(.headline)
                Spacer()
                if let moduleNotes = store.moduleNotes, let used = moduleNotes.used, let total = moduleNotes.total {
                    Text("通讯录 \(used)/\(total)")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            if let error = store.notesError {
                Label("本地备注读取失败", systemImage: "exclamationmark.triangle")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .help(error)
            }
            if let error = store.moduleNotesError {
                Label("模块资料库暂不可用", systemImage: "exclamationmark.triangle")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .help(error)
            }
            ForEach(Array(store.profileGroups.enumerated()), id: \.offset) { _, item in
                if store.profileGroups.count > 1 {
                    Text(euiccGroupTitle(item.group))
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(.secondary)
                        .padding(.top, 2)
                }
                if item.group.readState == "error" {
                    Label(item.group.readError ?? "Profile 列表读取失败", systemImage: "exclamationmark.triangle")
                        .font(.callout)
                        .foregroundStyle(.orange)
                        .padding(.vertical, 8)
                }
                ForEach(item.profiles) { context in
                    ProfileRow(
                        context: context,
                        note: store.notes[context.profile.iccid],
                        isEditing: editingProfile?.id == context.id,
                        isBusy: store.isBusy,
                        canSwitch: store.capabilities?.canSwitch == true,
                        canRename: store.capabilities?.canRename == true,
                        canDelete: store.capabilities?.canDelete == true,
                        isActionRunning: store.isProfileActionRunning(context)
                    ) { action in
                        handle(context, action)
                    }
                }
            }
        }
    }

    private var emptyProfiles: some View {
        VStack(spacing: 8) {
            Image(systemName: "tray").font(.largeTitle).foregroundStyle(.tertiary)
            Text("没有已安装的 Profile").foregroundStyle(.secondary)
            Text("可通过“下载 Profile”添加。")
                .font(.caption).foregroundStyle(.tertiary)
            Button("下载 Profile") { showDownload = true }
                .buttonStyle(.borderedProminent)
                .disabled(store.isBusy || store.capabilities?.canDownload != true)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 32)
    }

    private var physicalSIMEmptyState: some View {
        VStack(spacing: 8) {
            Image(systemName: "simcard").font(.largeTitle).foregroundStyle(.tertiary)
            Text("当前是实体 SIM").font(.headline)
            Text("实体 SIM 没有可下载或切换的 eSIM Profile。")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 32)
    }

    private var hasProfileReadError: Bool {
        store.profileGroups.contains { $0.group.readState == "error" }
    }

    private func handle(_ context: ESIMProfileContext, _ action: ProfileAction) {
        switch action {
        case .enable:
            enablingProfile = context
        case .editNote:
            editingProfile = context
        case .rename:
            renamingProfile = context
        case .delete:
            deletingProfile = context
        }
    }

    private func loadFailure(_ message: String) -> some View {
        VStack(spacing: 10) {
            Image(systemName: "simcard").font(.largeTitle).foregroundStyle(.tertiary)
            Text("eSIM 信息不可用").font(.headline)
            Text(message)
                .font(.callout)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 460)
            Button("重试") { store.load() }
                .buttonStyle(.borderedProminent)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func keyText(_ s: String) -> some View {
        Text(s).foregroundStyle(.secondary)
    }

    private func valueText(_ s: String) -> some View {
        Text(s)
            .textSelection(.enabled)
            .fixedSize(horizontal: false, vertical: true)
    }

    private func identityDescription(_ identity: CardIdentity) -> String {
        if identity.source == "card_private_aid" {
            return "卡片私有接口 · 已确认"
        }
        if identity.ruleID?.contains("sample") == true {
            return "EID 实卡样本与固件规则 · 较高可信度"
        }
        let confidence: String
        switch identity.confidence {
        case "high": confidence = "较高可信度"
        case "medium": confidence = "推断"
        case "confirmed": confidence = "已确认"
        default: confidence = "参考信息"
        }
        return "EID 与固件规则 · \(confidence)"
    }

    private func identitySourceLabel(_ identity: CardIdentity) -> String {
        let source = identity.sourceURL?.contains("NekokoLPA") == true
            ? "NekokoLPA/Vendors"
            : "OpenEUICC 兼容表"
        guard let version = identity.sourceVersion, !version.isEmpty else { return source }
        return "\(source) · \(version.prefix(8))"
    }

    private func identityEvidenceLabel(_ evidence: CardIdentityEvidence) -> String {
        guard !evidence.version.isEmpty else { return evidence.name }
        return "\(evidence.name) · \(evidence.version.prefix(8))"
    }

    private func euiccGroupTitle(_ group: EUICCProfiles) -> String {
        guard let eid = group.eid, eid.count > 8 else { return "eUICC" }
        return "eUICC · …\(eid.suffix(8))"
    }
}

private struct ChipDisclosureButtonStyle: ButtonStyle {
    let isHovered: Bool
    let reduceMotion: Bool

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .background(
                Color.primary.opacity(configuration.isPressed ? 0.07 : (isHovered ? 0.035 : 0))
            )
            .animation(reduceMotion ? nil : .easeOut(duration: 0.1), value: configuration.isPressed)
            .animation(reduceMotion ? nil : .easeOut(duration: 0.12), value: isHovered)
    }
}

enum ProfileAction {
    case enable
    case editNote
    case rename
    case delete
}

struct ProfileRow: View {
    let context: ESIMProfileContext
    let note: ProfileNote?
    let isEditing: Bool
    let isBusy: Bool
    let canSwitch: Bool
    let canRename: Bool
    let canDelete: Bool
    let isActionRunning: Bool
    let onAction: (ProfileAction) -> Void

    private var profile: ProfileItem { context.profile }

    var body: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(profile.displayName)
                        .font(.headline)
                    if profile.isEnabled {
                        Text("已启用")
                            .font(.caption2.bold())
                            .padding(.horizontal, 6).padding(.vertical, 2)
                            .background(Capsule().fill(Color.green.opacity(0.2)))
                            .foregroundStyle(.green)
                    }
                }
                if let spn = profile.serviceProviderName, !spn.isEmpty {
                    Text(spn).font(.caption).foregroundStyle(.secondary)
                }
                HStack(spacing: 10) {
                    Text("ICCID \(profile.iccid)")
                        .font(.caption.monospaced()).foregroundStyle(.secondary)
                    if let note, let label = note.label, !label.isEmpty {
                        Text("备注：\(label)\(note.phone.map { " · \($0)" } ?? "")")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            Spacer()
            HStack(spacing: 8) {
                if !profile.isEnabled {
                    Button {
                        onAction(.enable)
                    } label: {
                        if isActionRunning {
                            ProgressView().controlSize(.small)
                        } else {
                            Text("启用")
                        }
                    }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                        .disabled(isBusy || !canSwitch)
                }
                Button { onAction(.editNote) } label: {
                    Image(systemName: "person.crop.circle.badge.plus")
                }
                .controlSize(.small)
                .help("编辑号码资料")
                .accessibilityLabel("编辑号码资料")
                .disabled(isBusy)
                Button { onAction(.rename) } label: {
                    Image(systemName: "pencil")
                }
                .controlSize(.small)
                .help("修改名称")
                .accessibilityLabel("修改 Profile 名称")
                .disabled(isBusy || !canRename)
                Button { onAction(.delete) } label: {
                    Image(systemName: "trash")
                }
                .controlSize(.small)
                .foregroundStyle(.red)
                .help(profile.isEnabled ? "已启用的 Profile 不能删除，请先切换到其他 Profile" : "删除 Profile")
                .accessibilityLabel("删除 Profile")
                .disabled(isBusy || !canDelete || profile.isEnabled)
            }
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(
            isEditing ? Color.accentColor : Color(nsColor: .separatorColor),
            lineWidth: isEditing ? 2 : 1))
        .opacity(profile.isEnabled ? 1 : 0.75)
        .accessibilityElement(children: .contain)
    }
}

struct ESIMStatusBanner: View {
    let banner: ESIMBanner
    let onDismiss: () -> Void

    private var icon: String {
        switch banner.tone {
        case .info: return "info.circle.fill"
        case .success: return "checkmark.circle.fill"
        case .warning: return "exclamationmark.triangle.fill"
        case .error: return "xmark.octagon.fill"
        }
    }

    private var color: Color {
        switch banner.tone {
        case .info: return .accentColor
        case .success: return .green
        case .warning: return .orange
        case .error: return .red
        }
    }

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: icon)
                .foregroundStyle(color)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(banner.message).font(.callout.weight(.semibold))
                if let detail = banner.detail, !detail.isEmpty {
                    Text(detail).font(.caption).foregroundStyle(.secondary)
                }
            }
            Spacer(minLength: 8)
            Button(action: onDismiss) {
                Image(systemName: "xmark")
            }
            .buttonStyle(.plain)
            .accessibilityLabel("关闭提示")
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(color.opacity(0.35), lineWidth: 1))
    }
}

struct ESIMOperationBanner: View {
    let operation: ESIMOperationSnapshot

    var body: some View {
        HStack(spacing: 10) {
            ProgressView(value: Double(operation.progress), total: 100)
                .frame(width: 110)
                .accessibilityLabel("eSIM 操作进度")
                .accessibilityValue("\(operation.progress)%")
            VStack(alignment: .leading, spacing: 2) {
                Text(operation.message ?? "eSIM 操作进行中…")
                    .font(.callout.weight(.semibold))
                Text("\(operation.progress)%")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding(10)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.accentColor.opacity(0.35), lineWidth: 1))
    }
}

struct HoverStatusIndicator: View {
    let systemImage: String
    let color: Color
    let title: String
    let message: String

    @State private var isPopoverPresented = false

    var body: some View {
        Image(systemName: systemImage)
            .foregroundStyle(color)
            .frame(width: 20, height: 20)
            .contentShape(Rectangle())
            .onHover { isPopoverPresented = $0 }
            .popover(isPresented: $isPopoverPresented) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(title)
                        .font(.callout.weight(.semibold))
                    Text(message)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(12)
                .frame(width: 300, alignment: .leading)
            }
            .help(message)
            .accessibilityLabel("\(title)：\(message)")
    }
}

// MARK: - 下载 Profile

private enum ActivationCodeMessageTone {
    case info
    case success
    case warning

    var systemImage: String {
        switch self {
        case .info: return "info.circle"
        case .success: return "checkmark.circle.fill"
        case .warning: return "exclamationmark.triangle.fill"
        }
    }

    var iconColor: Color {
        switch self {
        case .info: return .secondary
        case .success: return .green
        case .warning: return .orange
        }
    }

    var textColor: Color {
        switch self {
        case .info: return .secondary
        case .success, .warning: return .primary
        }
    }
}

struct DownloadESIMView: View {
    @Environment(\.dismiss) private var dismiss
    @ObservedObject var store: ESIMStore

    @State private var activationCode = ""
    @State private var smdp = ""
    @State private var matchingID = ""
    @State private var confirmationCode = ""
    @State private var selectedAID = ""
    @State private var didStart = false
    @State private var operationIDBeforeStart: String?
    @State private var parseMessage: String?
    @State private var parseMessageTone: ActivationCodeMessageTone = .info
    @State private var showQRCodeImageImporter = false
    @State private var showQRCodeScanner = false
    @State private var isQRCodeDropTargeted = false
    @State private var isImportingQRCode = false
    @State private var suppressAutomaticParse = false

    private var downloadOperation: ESIMOperationSnapshot? {
        guard didStart,
              let operation = store.operation,
              operation.kind == "download_profile",
              operation.id != operationIDBeforeStart else { return nil }
        return operation
    }

    private var isSubmitting: Bool {
        store.action == .downloading && downloadOperation == nil
    }

    private var isActive: Bool {
        isSubmitting || downloadOperation?.isActive == true
    }

    private var targetGroups: [EUICCProfiles] {
        (store.overview?.profiles ?? []).filter { !($0.aidHex ?? "").isEmpty }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("下载新的 Profile").font(.headline)
            Text("可粘贴激活码、导入二维码图片或使用摄像头扫描，也可以手动填写连接信息。写入过程请勿拔出模块。")
                .font(.caption).foregroundStyle(.secondary)

            GroupBox("激活码") {
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        TextField("LPA:1$SM-DP+$Matching ID", text: $activationCode)
                            .textFieldStyle(.roundedBorder)
                            .onChange(of: activationCode) { value in
                                if !suppressAutomaticParse,
                                   value.trimmingCharacters(in: .whitespacesAndNewlines)
                                    .uppercased().hasPrefix("LPA:") {
                                    parseActivationCode(value, showErrors: false)
                                }
                            }
                        Button("解析") { parseActivationCode(showErrors: true) }
                            .disabled(activationCode.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    }

                    HStack(spacing: 8) {
                        Button(action: importFromPasteboard) {
                            Label("从剪贴板", systemImage: "doc.on.clipboard")
                        }
                        Button {
                            showQRCodeImageImporter = true
                        } label: {
                            Label("选择图片", systemImage: "photo")
                        }
                        Button {
                            showQRCodeScanner = true
                        } label: {
                            Label("摄像头扫描", systemImage: "camera.viewfinder")
                        }
                        Spacer(minLength: 0)
                    }
                    .controlSize(.small)
                    .disabled(isActive || isImportingQRCode)

                    Label(
                        isQRCodeDropTargeted ? "松开即可识别二维码" : "也可以把二维码图片或激活码拖到这里",
                        systemImage: isQRCodeDropTargeted ? "arrow.down.circle.fill" : "square.and.arrow.down")
                        .font(.caption)
                        .foregroundStyle(isQRCodeDropTargeted ? Color.accentColor : Color.secondary)

                    if let parseMessage {
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            if isImportingQRCode {
                                ProgressView()
                                    .controlSize(.mini)
                            } else {
                                Image(systemName: parseMessageTone.systemImage)
                                    .foregroundStyle(parseMessageTone.iconColor)
                                    .accessibilityHidden(true)
                            }
                            Text(parseMessage)
                                .foregroundStyle(parseMessageTone.textColor)
                        }
                        .font(.caption)
                    }
                }
                .padding(.top, 2)
            }
            .background(
                RoundedRectangle(cornerRadius: 8)
                    .fill(Color.accentColor.opacity(isQRCodeDropTargeted ? 0.06 : 0))
            )
            .overlay(
                RoundedRectangle(cornerRadius: 8)
                    .stroke(isQRCodeDropTargeted ? Color.accentColor : Color.clear, lineWidth: 2)
            )
            .onDrop(
                of: [UTType.image.identifier, UTType.fileURL.identifier, UTType.plainText.identifier],
                isTargeted: $isQRCodeDropTargeted,
                perform: handleQRCodeDrop)

            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 10) {
                GridRow {
                    Text("SM-DP+ 地址")
                    TextField("smdp.example.com", text: $smdp)
                        .textFieldStyle(.roundedBorder)
                }
                GridRow {
                    Text("Matching ID")
                    TextField("可选", text: $matchingID)
                        .textFieldStyle(.roundedBorder)
                }
                GridRow {
                    Text("确认码")
                    SecureField("可选", text: $confirmationCode)
                        .textFieldStyle(.roundedBorder)
                }
                if targetGroups.count > 1 {
                    GridRow {
                        Text("目标 eUICC")
                        Picker("", selection: $selectedAID) {
                            ForEach(Array(targetGroups.enumerated()), id: \.offset) { index, group in
                                Text(targetTitle(group, index: index)).tag(group.aidHex ?? "")
                            }
                        }
                        .labelsHidden()
                    }
                }
            }
            .font(.callout)

            if let operation = downloadOperation {
                operationStatus(operation)
            } else if didStart, let banner = store.banner {
                Label(banner.detail ?? banner.message, systemImage: "xmark.octagon.fill")
                    .font(.callout)
                    .foregroundStyle(.red)
            }

            HStack {
                Button("取消") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                    .disabled(isActive)
                Spacer()
                if let operation = downloadOperation, !operation.isActive {
                    Button("完成") { dismiss() }
                        .buttonStyle(.borderedProminent)
                        .keyboardShortcut(.defaultAction)
                } else {
                    Button {
                        download()
                    } label: {
                        if isActive {
                            ProgressView().controlSize(.small)
                        } else {
                            Text("下载并安装")
                        }
                    }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .disabled(isActive || smdp.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
        .padding(20)
        .frame(width: 540)
        .interactiveDismissDisabled(isActive)
        .fileImporter(
            isPresented: $showQRCodeImageImporter,
            allowedContentTypes: [.image],
            onCompletion: handleQRCodeFileSelection)
        .sheet(isPresented: $showQRCodeScanner) {
            ESIMQRCodeScannerSheet { code in
                applyImportedActivationCode(code, source: "摄像头")
            }
        }
        .onAppear {
            if selectedAID.isEmpty {
                selectedAID = targetGroups.first?.aidHex ?? ""
            }
        }
    }

    private func download() {
        operationIDBeforeStart = store.operation?.id
        didStart = true
        store.startDownload(ESIMDownloadRequest(
            smdp: smdp.trimmingCharacters(in: .whitespacesAndNewlines),
            matchingID: optionalTrimmed(matchingID),
            confirmationCode: optionalTrimmed(confirmationCode),
            aid: selectedAID.isEmpty ? nil : selectedAID,
            imei: nil))
    }

    private func importFromPasteboard() {
        let pasteboard = NSPasteboard.general
        if let text = pasteboard.string(forType: .string),
           let code = ESIMQRCodeDecoder.activationCode(from: text) {
            applyImportedActivationCode(code, source: "剪贴板")
            return
        }

        if let data = pasteboard.data(forType: .png) ?? pasteboard.data(forType: .tiff) {
            recognizeQRCode(in: data, source: "剪贴板图片")
            return
        }

        if let image = NSImage(pasteboard: pasteboard), let data = image.tiffRepresentation {
            recognizeQRCode(in: data, source: "剪贴板图片")
            return
        }

        if let rawURL = pasteboard.string(forType: .fileURL), let url = URL(string: rawURL) {
            importQRCode(at: url, source: "剪贴板图片")
            return
        }

        setImportMessage("剪贴板中没有 eSIM 激活码或二维码图片", tone: .warning)
    }

    private func handleQRCodeFileSelection(_ result: Result<URL, Error>) {
        switch result {
        case .success(let url):
            importQRCode(at: url, source: "所选图片")
        case .failure(let error):
            let nsError = error as NSError
            guard nsError.code != NSUserCancelledError else { return }
            setImportMessage("无法读取所选图片：\(error.localizedDescription)", tone: .warning)
        }
    }

    private func handleQRCodeDrop(_ providers: [NSItemProvider]) -> Bool {
        guard !isActive, !isImportingQRCode else { return false }

        if let provider = providers.first(where: {
            $0.hasItemConformingToTypeIdentifier(UTType.image.identifier)
        }) {
            provider.loadDataRepresentation(forTypeIdentifier: UTType.image.identifier) { data, error in
                DispatchQueue.main.async {
                    if let data {
                        recognizeQRCode(in: data, source: "拖入的图片")
                    } else {
                        setImportMessage(
                            "无法读取拖入的图片：\(error?.localizedDescription ?? "未知错误")",
                            tone: .warning)
                    }
                }
            }
            return true
        }

        if let provider = providers.first(where: {
            $0.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier)
        }) {
            provider.loadItem(forTypeIdentifier: UTType.fileURL.identifier, options: nil) { item, error in
                let url: URL?
                if let item = item as? URL {
                    url = item
                } else if let item = item as? NSURL {
                    url = item as URL
                } else if let data = item as? Data {
                    url = URL(dataRepresentation: data, relativeTo: nil)
                } else {
                    url = nil
                }
                DispatchQueue.main.async {
                    if let url {
                        importQRCode(at: url, source: "拖入的图片")
                    } else {
                        setImportMessage(
                            "无法读取拖入的文件：\(error?.localizedDescription ?? "文件格式不受支持")",
                            tone: .warning)
                    }
                }
            }
            return true
        }

        if let provider = providers.first(where: {
            $0.hasItemConformingToTypeIdentifier(UTType.plainText.identifier)
        }) {
            provider.loadItem(forTypeIdentifier: UTType.plainText.identifier, options: nil) { item, error in
                let text: String?
                if let item = item as? String {
                    text = item
                } else if let item = item as? NSString {
                    text = item as String
                } else if let data = item as? Data {
                    text = String(data: data, encoding: .utf8)
                } else {
                    text = nil
                }
                DispatchQueue.main.async {
                    if let text, let code = ESIMQRCodeDecoder.activationCode(from: text) {
                        applyImportedActivationCode(code, source: "拖入内容")
                    } else {
                        setImportMessage(
                            error.map { "无法读取拖入内容：\($0.localizedDescription)" }
                                ?? "拖入的内容不是 eSIM 激活码",
                            tone: .warning)
                    }
                }
            }
            return true
        }

        return false
    }

    private func importQRCode(at url: URL, source: String) {
        let hasSecurityScope = url.startAccessingSecurityScopedResource()
        defer {
            if hasSecurityScope {
                url.stopAccessingSecurityScopedResource()
            }
        }
        do {
            recognizeQRCode(in: try Data(contentsOf: url, options: .mappedIfSafe), source: source)
        } catch {
            setImportMessage("无法读取图片：\(error.localizedDescription)", tone: .warning)
        }
    }

    private func recognizeQRCode(in data: Data, source: String) {
        guard !isImportingQRCode else { return }
        isImportingQRCode = true
        setImportMessage("正在识别\(source)中的二维码…", tone: .info)

        Task {
            let result = await Task.detached(priority: .userInitiated) { () -> Result<String, ESIMQRCodeError> in
                do {
                    return .success(try ESIMQRCodeDecoder.activationCode(in: data))
                } catch let error as ESIMQRCodeError {
                    return .failure(error)
                } catch {
                    return .failure(.recognitionFailed(error.localizedDescription))
                }
            }.value

            await MainActor.run {
                isImportingQRCode = false
                switch result {
                case .success(let code):
                    applyImportedActivationCode(code, source: source)
                case .failure(let error):
                    setImportMessage(error.localizedDescription, tone: .warning)
                }
            }
        }
    }

    private func applyImportedActivationCode(_ rawValue: String, source: String) {
        guard let code = ESIMQRCodeDecoder.activationCode(from: rawValue) else {
            setImportMessage("识别到的内容不是 eSIM 激活码", tone: .warning)
            return
        }
        suppressAutomaticParse = true
        activationCode = code
        parseActivationCode(code, showErrors: true, source: source)
        DispatchQueue.main.async {
            suppressAutomaticParse = false
        }
    }

    private func setImportMessage(_ message: String, tone: ActivationCodeMessageTone) {
        parseMessage = message
        parseMessageTone = tone
    }

    private func parseActivationCode(showErrors: Bool) {
        parseActivationCode(activationCode, showErrors: showErrors)
    }

    private func parseActivationCode(_ rawValue: String, showErrors: Bool, source: String? = nil) {
        let raw = rawValue.trimmingCharacters(in: .whitespacesAndNewlines)
        let parts = raw.split(separator: "$", omittingEmptySubsequences: false).map(String.init)
        guard parts.count >= 3, parts[0].uppercased() == "LPA:1" else {
            if showErrors {
                setImportMessage("激活码格式不正确，应以 LPA:1$ 开头", tone: .warning)
            }
            return
        }
        let parsedSMDP = parts[1].trimmingCharacters(in: .whitespacesAndNewlines)
        let parsedMatchingID = parts[2].trimmingCharacters(in: .whitespacesAndNewlines)
        guard !parsedSMDP.isEmpty, !parsedMatchingID.isEmpty else {
            if showErrors {
                setImportMessage("激活码缺少 SM-DP+ 地址或 Matching ID", tone: .warning)
            }
            return
        }
        smdp = parsedSMDP
        matchingID = parsedMatchingID
        let successMessage = source.map { "已从\($0)识别并解析激活码" }
            ?? "已解析 SM-DP+ 地址和 Matching ID"
        if parts.count > 3 {
            setImportMessage("\(successMessage)；扩展字段未自动用作确认码", tone: .warning)
        } else {
            setImportMessage(successMessage, tone: .success)
        }
    }

    @ViewBuilder
    private func operationStatus(_ operation: ESIMOperationSnapshot) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            if operation.isActive {
                ProgressView(value: Double(operation.progress), total: 100)
                Text(operation.message ?? "正在下载 Profile…")
                    .font(.callout)
                Text("\(operation.progress)%")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            } else if operation.isFailed {
                Label(operation.error ?? "Profile 下载失败", systemImage: "xmark.octagon.fill")
                    .foregroundStyle(.red)
            } else if operation.hasWarning {
                Label(operation.result?.warning ?? operation.message ?? "下载完成，但需要检查卡片状态", systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
            } else {
                Label(operation.message ?? "Profile 下载完成", systemImage: "checkmark.circle.fill")
                    .foregroundStyle(.green)
            }
        }
        .font(.callout)
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 8).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }

    private func targetTitle(_ group: EUICCProfiles, index: Int) -> String {
        guard let eid = group.eid, !eid.isEmpty else { return "eUICC \(index + 1)" }
        return "eUICC \(index + 1) · …\(eid.suffix(8))"
    }

    private func optionalTrimmed(_ value: String) -> String? {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }
}

// MARK: - 改名

struct RenameProfileView: View {
    @Environment(\.dismiss) private var dismiss
    let context: ESIMProfileContext
    let onDone: () -> Void

    @State private var name: String
    @State private var busy = false
    @State private var errorMessage: String?

    init(context: ESIMProfileContext, onDone: @escaping () -> Void) {
        self.context = context
        self.onDone = onDone
        _name = State(initialValue: context.profile.name ?? "")
    }

    private var trimmedName: String {
        name.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("修改 Profile 名称").font(.headline)
            TextField("名称", text: $name)
                .textFieldStyle(.roundedBorder)
            Text("最多 64 个字符")
                .font(.caption)
                .foregroundStyle(.secondary)
            if trimmedName.count > 64 {
                Text("当前为 \(trimmedName.count) 个字符，请缩短名称。")
                    .font(.caption)
                    .foregroundStyle(.red)
            }
            if let errorMessage {
                Text(errorMessage).font(.callout).foregroundStyle(.red)
            }
            HStack {
                Button("取消") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                    .disabled(busy)
                Spacer()
                Button {
                    save()
                } label: {
                    if busy {
                        ProgressView().controlSize(.small)
                    } else {
                        Text("保存")
                    }
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .disabled(busy || trimmedName.isEmpty || trimmedName.count > 64)
            }
        }
        .padding(20)
        .frame(width: 400)
        .interactiveDismissDisabled(busy)
    }

    private func save() {
        busy = true
        Task {
            do {
                let _: MessageResponse = try await APIClient().send(
                    "api/esim/profile", method: "PATCH",
                    body: ESIMRenameRequest(
                        iccid: context.profile.iccid,
                        aid: context.aidHex,
                        name: trimmedName))
                await MainActor.run {
                    busy = false
                    onDone()
                    dismiss()
                }
            } catch {
                await MainActor.run {
                    busy = false
                    errorMessage = error.localizedDescription
                }
            }
        }
    }
}

// MARK: - 号码资料

struct ProfileNoteView: View {
    @Environment(\.dismiss) private var dismiss
    let context: ESIMProfileContext
    let localNote: ProfileNote?
    let moduleNote: ModuleProfileNote?
    let moduleStatus: String?
    let moduleUnavailableReason: String?
    let onDone: () -> Void

    @State private var label: String
    @State private var phone: String
    @State private var tags: String
    @State private var saveToModule: Bool
    @State private var busy = false
    @State private var statusMessage: String?
    @State private var statusIsError = false

    init(context: ESIMProfileContext, localNote: ProfileNote?, moduleNote: ModuleProfileNote?, moduleStatus: String?, moduleUnavailableReason: String?, onDone: @escaping () -> Void) {
        self.context = context
        self.localNote = localNote
        self.moduleNote = moduleNote
        self.moduleStatus = moduleStatus
        self.moduleUnavailableReason = moduleUnavailableReason
        self.onDone = onDone
        _label = State(initialValue: localNote?.label ?? moduleNote?.label ?? "")
        _phone = State(initialValue: localNote?.phone ?? moduleNote?.phone ?? "")
        _tags = State(initialValue: localNote?.tags ?? moduleNote?.tags ?? "")
        _saveToModule = State(initialValue: moduleStatus != nil)
    }

    private var profile: ProfileItem { context.profile }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("号码资料").font(.headline)
            Text("本地资料按 ICCID 与 \(profile.displayName) 关联。")
                .font(.caption).foregroundStyle(.secondary)

            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 10) {
                GridRow {
                    Text("名称")
                    TextField("本地最多 80 个字符", text: $label)
                        .textFieldStyle(.roundedBorder)
                }
                GridRow {
                    Text("电话号码")
                    TextField("如 +86138XXXXXXXX", text: $phone)
                        .textFieldStyle(.roundedBorder)
                }
                GridRow {
                    Text("标签")
                    TextField("多个标签可用逗号分隔", text: $tags)
                        .textFieldStyle(.roundedBorder)
                }
            }
            .font(.callout)

            Toggle("同时保存到模块资料库", isOn: $saveToModule)
                .disabled(moduleStatus == nil || busy)

            if let moduleStatus {
                Text("模块资料库容量：\(moduleStatus)；模块字段上限为名称 48、号码 40、标签 48 个字符。")
                    .font(.caption).foregroundStyle(.secondary)
            } else {
                Text(moduleUnavailableReason.map { "模块资料库当前不可用：\($0)" } ?? "模块资料库当前不可用，本次仅保存到本机。")
                    .font(.caption).foregroundStyle(.secondary)
            }

            if let fieldLimitMessage {
                Label(fieldLimitMessage, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.red)
            }

            if let statusMessage {
                Text(statusMessage)
                    .font(.callout)
                    .foregroundStyle(statusIsError ? .red : .orange)
            }

            HStack {
                Button("清空") {
                    label = ""; phone = ""; tags = ""
                }
                .disabled(busy)
                Spacer()
                Button("取消") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                    .disabled(busy)
                Button {
                    save()
                } label: {
                    if busy {
                        ProgressView().controlSize(.small)
                    } else {
                        Text("保存")
                    }
                }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .disabled(busy || !fieldsFitSelectedDestinations)
            }
        }
        .padding(20)
        .frame(width: 500)
        .interactiveDismissDisabled(busy)
    }

    private var fieldsFitSelectedDestinations: Bool {
        fieldLimitMessage == nil
    }

    private var fieldLimitMessage: String? {
        if label.count > 80 || phone.count > 80 || tags.count > 200 {
            return "本地字段超过上限（名称 80、号码 80、标签 200）。"
        }
        if saveToModule && (label.count > 48 || phone.count > 40 || tags.count > 48) {
            return "模块字段超过上限（名称 48、号码 40、标签 48）。"
        }
        return nil
    }

    private func save() {
        busy = true
        statusMessage = nil
        Task {
            let client = APIClient()
            var localError: String?
            var moduleError: String?
            var localSaved = false
            var moduleSaved = false

            do {
                let _: MessageResponse = try await client.send(
                    "api/esim/notes", method: "PUT",
                    body: SaveNoteRequest(
                        iccid: context.profile.iccid,
                        label: label,
                        phone: phone,
                        tags: tags))
                localSaved = true
            } catch {
                localError = error.localizedDescription
            }

            if saveToModule {
                do {
                    let _: MessageResponse = try await client.send(
                        "api/esim/module-notes", method: "PUT",
                        body: SaveModuleNoteRequest(
                            iccid: context.profile.iccid,
                            label: label,
                            phone: phone,
                            tags: tags))
                    moduleSaved = true
                } catch {
                    moduleError = error.localizedDescription
                }
            }

            await MainActor.run {
                busy = false
                if localSaved && (!saveToModule || moduleSaved) {
                    onDone()
                    dismiss()
                    return
                }
                if localSaved {
                    statusMessage = "本地资料已保存；模块写入失败：\(moduleError ?? "未知错误")"
                    statusIsError = false
                    onDone()
                } else if moduleSaved {
                    statusMessage = "模块资料已保存；本地保存失败：\(localError ?? "未知错误")"
                    statusIsError = false
                    onDone()
                } else {
                    let failures = [localError.map { "本地：\($0)" }, moduleError.map { "模块：\($0)" }]
                        .compactMap { $0 }
                        .joined(separator: "；")
                    statusMessage = failures.isEmpty ? "保存失败" : failures
                    statusIsError = true
                }
            }
        }
    }
}
