---
version: 1
slug: "app-sources-views-callview-swift"
primary_target: "app/Sources/Views/CallView.swift"
related_targets: ["app/Sources/DashboardStore.swift","app/Sources/Views/ContentView.swift"]
---

Scope: dedicated call page in operate mode.

Audience and job: a DJI One module owner preparing call audio for the first time, then dialing, answering, monitoring, and ending everyday cellular calls from macOS.

Primary task: make readiness unambiguous while making the everyday phone action visually dominant. Persistent ADB authorization, USB changes, module restart, and third-party downloads always require explicit confirmation.

Content and proof: distinguish persistent ADB authorization from reversible ADB/UAC and IMS/VoLTE configuration; show authorization/write/restart/verification stages, local runtime version/path, byte progress, real root-interface readiness, current call/audio state, real call history, and module-bound configuration backups with export/restore eligibility. Never claim compatibility beyond the detected 3.18.44/QDC507 module.

Chosen direction: a desktop call workbench with one continuous call stage and persistent operational evidence. Wide windows use two independent vertical tracks: dialing plus ringtone on the left, readiness plus module-bound backups on the right. Default and narrow windows use one task-priority column: readiness leads while setup is incomplete, and the call stage leads after readiness. The call stage keeps number entry and recent calls beside a compact keypad where width allows; live call state replaces that workbench in place. Approved comp: `.impeccable/mocks/call-workspace-b.png` remains the visual reference, while the responsive topology follows the current native implementation.

Memorable moment: the same stable main surface changes from dialing to the current call without moving the operational evidence; a recent number can be called again directly from its row.

Implementation grammar: system type ramp (`title2`, `headline`, `body/callout`, `caption`); semantic colors; 10pt outer corners; 1pt separator outlines; no shadow; one bordered main workbench and one bordered inspector; compact rectangular keypad keys; list rows separated by hairlines. Wide windows use independent main and operational tracks. Default and narrow windows use a single column, with only the minimum width stacking recent calls and the keypad vertically.

| Ingredient | Medium | Commitment |
| --- | --- | --- |
| Existing sidebar | SwiftUI `NavigationSplitView` | unchanged native navigation |
| Page header | SwiftUI text and SF Symbol | compact title plus visible readiness phrase |
| Dial/current-call stage | semantic SwiftUI | one continuous surface; state replaces in place |
| Number entry and call action | native `TextField` and `Button` | one obvious primary action |
| Keypad | SwiftUI grid | 3x4 compact rectangular controls, not enlarged phone circles |
| Recent calls | semantic SwiftUI rows | real data, direct callback, full history remains available |
| Operational inspector | semantic SwiftUI rows/disclosures | three persistent readiness rows; live audio remains in the call stage |
| Imagery | none | generated comps are composition references, not shipped assets |

Constraints: inherit DESIGN.md exactly; keyboard and VoiceOver access; no runtime bundling; original and relay source choices; hash verification; no automatic real call during setup; module restarts are explicit; restore requires matching IMEI and VID/PID, preserves schema-v2 USB-only semantics, and creates a fresh USB plus IMS/VoLTE safety backup before writing. Imported backups are validated before local storage, and deletion is confirmed. Unknown call state remains locked until refreshed.

Unresolved: real operator VoLTE behavior and host audio quality require a coordinated live call after automated readiness checks pass.
