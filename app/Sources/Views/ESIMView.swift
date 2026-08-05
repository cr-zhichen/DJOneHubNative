import SwiftUI

struct ESIMView: View {
    @EnvironmentObject private var backend: BackendProcess

    @State private var overview: ESIMOverview?
    @State private var health: ESIMHealth?
    @State private var status: DeviceStatus?
    @State private var notes: [String: ProfileNote] = [:]
    @State private var moduleNotes: ModuleNotesResponse?
    @State private var probe: PhonebookProbeResult?
    @State private var errorMessage: String?
    @State private var loading = false

    @State private var showDownload = false
    @State private var editingProfile: ProfileItem?
    @State private var renamingProfile: ProfileItem?
    @State private var deletingProfile: ProfileItem?

    var allProfiles: [ProfileItem] {
        overview?.profiles?.flatMap { $0.profiles ?? [] } ?? []
    }

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Divider()
            content
        }
        .navigationTitle("eSIM 卡片")
        .onAppear { load() }
        .sheet(isPresented: $showDownload) {
            DownloadESIMView(imei: status?.imei, onDone: { load() })
        }
        .sheet(item: $renamingProfile) { profile in
            RenameProfileView(profile: profile, onDone: { load() })
        }
        .sheet(item: $editingProfile) { profile in
            ProfileNoteView(
                profile: profile,
                localNote: notes[profile.iccid],
                moduleNote: moduleNotes?.notes?[profile.iccid],
                moduleStatus: moduleNotes.map { "\($0.used ?? 0)/\($0.total ?? 0)" },
                onDone: { load() })
        }
        .confirmationDialog("删除 Profile？", isPresented: Binding(
            get: { deletingProfile != nil },
            set: { if !$0 { deletingProfile = nil } }
        ), titleVisibility: .visible) {
            Button("删除 \(deletingProfile?.iccid ?? "")", role: .destructive) {
                deleteProfile(deletingProfile!)
            }
            Button("取消", role: .cancel) {}
        } message: {
            Text("删除 Profile 通常不可撤销。写入过程中不要拔出模块。")
        }
    }

    private var toolbar: some View {
        HStack(spacing: 10) {
            Text("Profile 管理").font(.headline)
            if let health {
                if let cardType = health.cardType {
                    Text(cardType == "physical_sim" ? "实体 SIM 卡" : "eUICC 卡")
                        .font(.caption)
                        .padding(.horizontal, 8).padding(.vertical, 3)
                        .background(Capsule().fill(Color.gray.opacity(0.2)))
                } else if health.ok == true {
                    Label("已启用 Profile", systemImage: "checkmark.circle.fill")
                        .font(.caption).foregroundStyle(.green)
                } else if let message = health.message {
                    Label(message, systemImage: "info.circle")
                        .font(.caption).foregroundStyle(.orange).lineLimit(1)
                }
            }
            Spacer()
            if let errorMessage {
                Text(errorMessage).font(.caption).foregroundStyle(.red).lineLimit(1)
            }
            Button {
                load()
            } label: {
                if loading {
                    ProgressView().controlSize(.small)
                } else {
                    Label("刷新", systemImage: "arrow.clockwise")
                }
            }
            .disabled(loading)
            Button {
                probePhonebook()
            } label: {
                Label("通讯录检测", systemImage: "book.closed")
            }
            .disabled(loading)
            Button {
                showDownload = true
            } label: {
                Label("下载 Profile", systemImage: "arrow.down.circle")
            }
            .buttonStyle(.borderedProminent)
            .disabled(loading)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    @ViewBuilder
    private var content: some View {
        if let errorMessage, overview == nil {
            VStack(spacing: 8) {
                Image(systemName: "simcard").font(.largeTitle).foregroundStyle(.tertiary)
                Text("eSIM 信息不可用").font(.headline)
                Text(errorMessage).font(.callout).foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let overview {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    if let message = overview.message, !message.isEmpty {
                        Text(message).font(.callout).foregroundStyle(.secondary)
                    }
                    chipSection(overview)
                    if !allProfiles.isEmpty {
                        profilesSection
                    } else if overview.cardType == "physical_sim" {
                        placeholderProfilesSection
                    } else {
                        emptyProfiles
                    }
                }
                .padding(16)
            }
            .scrollIndicators(.visible)
        } else {
            ProgressView("读取卡片信息…")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func chipSection(_ overview: ESIMOverview) -> some View {
        let chip = overview.chipInfo
        return VStack(alignment: .leading, spacing: 10) {
            Text("卡片信息").font(.headline)
            Grid(alignment: .leading, horizontalSpacing: 20, verticalSpacing: 6) {
                GridRow { keyText("型号"); valueText(chip?.skuName ?? "-") }
                GridRow { keyText("序列号"); valueText(chip?.serialNumber ?? "-") }
                GridRow { keyText("固件"); valueText(chip?.firmware ?? "-") }
                GridRow { keyText("EID"); valueText(chip?.eids?.first?.eid ?? "-") }
                GridRow { keyText("可用空间"); valueText(chip?.eids?.first?.freeNvram ?? "-") }
            }
            .font(.callout)
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 12).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(Color(nsColor: .separatorColor), lineWidth: 1))
    }

    private var profilesSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("已安装 Profile（\(allProfiles.count)）").font(.headline)
                Spacer()
                if let moduleNotes, let used = moduleNotes.used, let total = moduleNotes.total {
                    Text("通讯录 \(used)/\(total)")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            ForEach(allProfiles) { profile in
                ProfileRow(
                    profile: profile,
                    note: notes[profile.iccid],
                    isEditing: editingProfile?.iccid == profile.iccid
                ) { action in
                    handle(profile, action)
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
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 32)
    }

    /// 实体卡（非 eUICC）时的占位 Profile 区域，格式与有内容时一致
    private var placeholderProfilesSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("已安装 Profile（-）").font(.headline)
            HStack(spacing: 12) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("-").font(.headline)
                    HStack(spacing: 10) {
                        Text("ICCID -").font(.caption.monospaced()).foregroundStyle(.secondary)
                    }
                }
                Spacer()
            }
            .padding(12)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
            .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color(nsColor: .separatorColor), lineWidth: 1))
            .opacity(0.6)
        }
    }

    private func handle(_ profile: ProfileItem, _ action: ProfileAction) {
        switch action {
        case .enable:
            switchProfile(profile)
        case .editNote:
            editingProfile = profile
        case .rename:
            renamingProfile = profile
        case .delete:
            deletingProfile = profile
        }
    }

    // MARK: - 数据

    private func load() {
        guard case .running = backend.state else { return }
        loading = true
        let client = APIClient()
        Task {
            do {
                let fetched: ESIMOverview = try await client.get("api/esim")
                let notesRes: NotesResponse = try await client.get("api/esim/notes")
                let statusRes: DeviceStatus? = try? await client.get("api/status")
                var moduleRes: ModuleNotesResponse?
                var healthRes: ESIMHealth?
                // 这些端点在部分环境下可能不可用，单独容忍
                if let m = try? await client.get("api/esim/module-notes") as ModuleNotesResponse {
                    moduleRes = m
                }
                if let h = try? await client.get("api/esim/health") as ESIMHealth {
                    healthRes = h
                }
                await MainActor.run {
                    overview = fetched
                    status = statusRes
                    notes = notesRes.notes ?? [:]
                    moduleNotes = moduleRes
                    health = healthRes
                    errorMessage = nil
                    loading = false
                }
            } catch {
                await MainActor.run {
                    errorMessage = error.localizedDescription
                    loading = false
                }
            }
        }
    }

    private func switchProfile(_ profile: ProfileItem) {
        loading = true
        Task {
            do {
                let client = APIClient()
                let result: ESIMSwitchResult = try await client.send(
                    "api/esim/switch",
                    body: ESIMSwitchRequest(iccid: profile.iccid, aid: nil))
                await MainActor.run {
                    loading = false
                    errorMessage = result.moduleRebootWarning ?? result.phase == "done" ? nil : "切换返回：\(result.phase ?? "?")"
                }
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                load()
            } catch {
                await MainActor.run {
                    loading = false
                    errorMessage = error.localizedDescription
                }
            }
        }
    }

    private func deleteProfile(_ profile: ProfileItem) {
        loading = true
        Task {
            do {
                let client = APIClient()
                let result: ESIMProfileResult = try await client.send(
                    "api/esim/profile", method: "DELETE",
                    body: ESIMDeleteRequest(iccid: profile.iccid, aid: nil))
                await MainActor.run {
                    loading = false
                    errorMessage = result.warning ?? nil
                }
                load()
            } catch {
                await MainActor.run {
                    loading = false
                    errorMessage = error.localizedDescription
                }
            }
        }
    }

    private func probePhonebook() {
        loading = true
        Task {
            do {
                let client = APIClient()
                let result: PhonebookProbeResult = try await client.send("api/esim/phonebook/probe")
                await MainActor.run {
                    loading = false
                    let parts = [
                        result.storageSupported == true ? "存储支持" : "存储不支持",
                        result.storageSelected == true ? "已选中" : "未选中",
                        result.readSupported == true ? "可读" : "不可读",
                        result.writeSupported == true ? "可写" : "不可写",
                    ]
                    errorMessage = result.storageStatus.map { "通讯录：\(parts.joined(separator: "，"))（\($0)）" }
                }
            } catch {
                await MainActor.run {
                    loading = false
                    errorMessage = error.localizedDescription
                }
            }
        }
    }

    private func keyText(_ s: String) -> some View {
        Text(s).foregroundStyle(.secondary)
    }

    private func valueText(_ s: String) -> some View {
        Text(s).textSelection(.enabled)
    }
}

enum ProfileAction {
    case enable
    case editNote
    case rename
    case delete
}

struct ProfileRow: View {
    let profile: ProfileItem
    let note: ProfileNote?
    let isEditing: Bool
    let onAction: (ProfileAction) -> Void

    var body: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(profile.name ?? profile.iccid)
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
                    Button("启用") { onAction(.enable) }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                }
                Button { onAction(.editNote) } label: {
                    Image(systemName: "person.crop.circle.badge.plus")
                }
                .controlSize(.small)
                .help("编辑号码资料")
                Button { onAction(.rename) } label: {
                    Image(systemName: "pencil")
                }
                .controlSize(.small)
                .help("修改名称")
                Button { onAction(.delete) } label: {
                    Image(systemName: "trash")
                }
                .controlSize(.small)
                .foregroundStyle(.red)
                .help("删除 Profile")
            }
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 10).fill(Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(
            isEditing ? Color.accentColor : Color(nsColor: .separatorColor),
            lineWidth: isEditing ? 2 : 1))
        .opacity(profile.isEnabled ? 1 : 0.75)
    }
}

// MARK: - 下载 Profile

struct DownloadESIMView: View {
    @Environment(\.dismiss) private var dismiss
    let imei: String?
    let onDone: () -> Void

    @State private var smdp = ""
    @State private var matchingID = ""
    @State private var confirmationCode = ""
    @State private var downloading = false
    @State private var errorMessage: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("下载新的 Profile").font(.headline)
            Text("需要 SM-DP+ 地址和运营商提供的激活码。写入过程请勿拔出模块。")
                .font(.caption).foregroundStyle(.secondary)

            TextField("SM-DP+ 地址（如 smdp.example.com）", text: $smdp)
                .textFieldStyle(.roundedBorder)
            TextField("Matching ID（可选）", text: $matchingID)
                .textFieldStyle(.roundedBorder)
            TextField("确认码 Confirmation Code（可选）", text: $confirmationCode)
                .textFieldStyle(.roundedBorder)

            if let errorMessage {
                Text(errorMessage).font(.callout).foregroundStyle(.red)
            }

            HStack {
                Button("取消") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Spacer()
                Button {
                    download()
                } label: {
                    if downloading {
                        ProgressView().controlSize(.small)
                    } else {
                        Text("下载并安装")
                    }
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .disabled(downloading || smdp.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 480)
    }

    private func download() {
        downloading = true
        Task {
            do {
                let client = APIClient()
                let result: ESIMProfileResult = try await client.send(
                    "api/esim/download",
                    body: ESIMDownloadRequest(
                        smdp: smdp,
                        matchingID: matchingID.isEmpty ? nil : matchingID,
                        confirmationCode: confirmationCode.isEmpty ? nil : confirmationCode,
                        aid: nil,
                        imei: imei))
                await MainActor.run {
                    downloading = false
                    if let warning = result.warning, !warning.isEmpty {
                        errorMessage = warning
                    } else {
                        onDone()
                        dismiss()
                    }
                }
            } catch {
                await MainActor.run {
                    downloading = false
                    errorMessage = "下载失败：\(error.localizedDescription)"
                }
            }
        }
    }
}

// MARK: - 改名

struct RenameProfileView: View {
    @Environment(\.dismiss) private var dismiss
    let profile: ProfileItem
    let onDone: () -> Void

    @State private var name: String
    @State private var busy = false
    @State private var errorMessage: String?

    init(profile: ProfileItem, onDone: @escaping () -> Void) {
        self.profile = profile
        self.onDone = onDone
        _name = State(initialValue: profile.name ?? "")
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("修改 Profile 名称").font(.headline)
            TextField("名称", text: $name)
                .textFieldStyle(.roundedBorder)
            if let errorMessage {
                Text(errorMessage).font(.callout).foregroundStyle(.red)
            }
            HStack {
                Button("取消") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Spacer()
                Button("保存") { save() }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .disabled(busy || name.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 380)
    }

    private func save() {
        busy = true
        Task {
            do {
                let client = APIClient()
                let _: MessageResponse = try await client.send(
                    "api/esim/profile", method: "PATCH",
                    body: ESIMRenameRequest(iccid: profile.iccid, aid: nil, name: name))
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
    let profile: ProfileItem
    let localNote: ProfileNote?
    let moduleNote: ModuleProfileNote?
    let moduleStatus: String?
    let onDone: () -> Void

    @State private var label: String
    @State private var phone: String
    @State private var tags: String
    @State private var busy = false
    @State private var errorMessage: String?

    init(profile: ProfileItem, localNote: ProfileNote?, moduleNote: ModuleProfileNote?, moduleStatus: String?, onDone: @escaping () -> Void) {
        self.profile = profile
        self.localNote = localNote
        self.moduleNote = moduleNote
        self.moduleStatus = moduleStatus
        self.onDone = onDone
        _label = State(initialValue: localNote?.label ?? moduleNote?.label ?? "")
        _phone = State(initialValue: localNote?.phone ?? moduleNote?.phone ?? "")
        _tags = State(initialValue: localNote?.tags ?? moduleNote?.tags ?? "")
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("号码资料").font(.headline)
            Text("将号码保存到模块通讯录，按 ICCID 与 Profile 关联。\(profile.name ?? profile.iccid)")
                .font(.caption).foregroundStyle(.secondary)

            TextField("名称（≤48 字符）", text: $label)
                .textFieldStyle(.roundedBorder)
            TextField("电话号码（≤40 字符，如 +86138XXXXXXXX）", text: $phone)
                .textFieldStyle(.roundedBorder)
            TextField("标签（≤48 字符）", text: $tags)
                .textFieldStyle(.roundedBorder)

            if let moduleStatus {
                Text("模块通讯录容量：\(moduleStatus)")
                    .font(.caption).foregroundStyle(.secondary)
            }

            if let errorMessage {
                Text(errorMessage).font(.callout).foregroundStyle(.red)
            }

            HStack {
                Button("清空") {
                    label = ""; phone = ""; tags = ""
                }
                Spacer()
                Button("取消") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("保存") { save() }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .disabled(busy)
            }
        }
        .padding(20)
        .frame(width: 440)
    }

    private func save() {
        busy = true
        Task {
            do {
                let client = APIClient()
                let req = SaveModuleNoteRequest(
                    iccid: profile.iccid,
                    label: String(label.prefix(48)),
                    phone: String(phone.prefix(40)),
                    tags: String(tags.prefix(48)))
                let moduleResult: MessageResponse = try await client.send(
                    "api/esim/module-notes", method: "PUT", body: req)
                let localResult: MessageResponse = try await client.send(
                    "api/esim/notes", method: "PUT",
                    body: SaveNoteRequest(
                        iccid: profile.iccid,
                        label: String(label.prefix(80)),
                        phone: String(phone.prefix(80)),
                        tags: String(tags.prefix(200))))
                await MainActor.run {
                    busy = false
                    errorMessage = [moduleResult.message, localResult.message]
                        .compactMap { $0 }
                        .joined(separator: "；")
                    onDone()
                    dismiss()
                }
            } catch {
                await MainActor.run {
                    busy = false
                    errorMessage = "保存失败：\(error.localizedDescription)"
                }
            }
        }
    }
}
