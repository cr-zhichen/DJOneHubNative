# DJOneHub Interface Design

## Product character

DJOneHub is a focused macOS utility for operating a connected 4G module. The interface should feel native, compact, and operationally trustworthy: important state is visible, dangerous ambiguity is avoided, and the app does not imitate a consumer VPN client.

## Native visual system

- Use SwiftUI and AppKit semantic colors so light mode, dark mode, increased contrast, and accent-color preferences remain correct. Do not replace semantic colors with fixed hex values.
- Use the macOS system typeface and standard SwiftUI text roles. Page titles use `title2.bold`, panel titles use `headline`, body controls use `body` or `callout`, and supporting copy uses `caption`.
- Use an 18-point page inset, 14-point spacing between page sections, and 14-point panel padding.
- Panels use a 10-point corner radius, `controlBackgroundColor`, and a 1-point `separatorColor` outline. Avoid decorative shadows and nested card stacks.
- Prefer native controls (`NavigationSplitView`, sidebar `List`, segmented `Picker`, `Toggle`, bordered buttons, and standard text fields) over custom replicas.
- Use semantic color on status icons; keep error and warning text in the primary foreground color for readable contrast. Secondary color is reserved for explanatory copy.

## Information architecture

- Major capabilities live as dedicated sidebar pages. Application routing is owned by the `应用分流` page and is not embedded into the dashboard or module settings.
- A page begins with one title, a short scope statement, and its primary runtime control. Configuration follows in bordered panels.
- Runtime status, readiness, conflicts, and saved-state feedback stay near the mode selector and enable control.

## Application routing interaction contract

- Routing is off after every DJOneHub launch. Configuration persists; enabled runtime state does not.
- Independent routing installs its privileged TUN service on first enable. Ordinary Stop and app exit stop the TUN but keep that service installed, so later enables do not request authorization again.
- A secondary destructive `卸载…` action appears only when the TUN service is installed. It confirms intent, stops an active independent session first, removes all privileged components, and preserves routing configuration; the next enable requests administrator authorization again.
- Independent routing exposes one explicit default exit. Per-application rules override it; existing version 1 configurations migrate to `系统直连` without dropping application rules.
- The two modes are mutually exclusive:
  - `独立分流` owns the single TUN and application rules. Its default exit can be `系统直连`, `4G 直连`, or `系统侧 SOCKS`; per-application rules remain explicit overrides.
  - Independent routing uses the gVisor TUN stack on macOS for both TCP and UDP. This avoids the mixed stack's system-TCP NAT path, which can drop IPv4 TCP before an outbound rule is selected.
  - `4G 直连` is dual-stack. The generated core configuration binds IPv4 and any available global IPv6 source address to the module interface; DNS from module-routed applications is handled by the module gateway so unsupported record types on the system resolver cannot stall Chromium. Preflight reports when the module has no usable IPv6 address or scoped default route.
  - The OpenClash/Clash Fake-IP benchmark range `198.18.0.0/15` bypasses the DJOneHub TUN and remains owned by the system network. This preserves an upstream router's transparent proxy mapping and avoids re-encapsulating unlisted applications' QUIC traffic.
  - A loopback `系统侧 SOCKS` listener is resolved to its owning process before start whenever that process would otherwise re-enter the TUN. The process is routed through `系统直连`; an unbound port or unresolved process blocks start instead of creating a proxy loop.
  - `Clash 代管` creates no DJOneHub TUN. It exposes a loopback SOCKS5 endpoint bound outward to the 4G interface, while Clash owns application selection and TUN routing.
- Editing, saving, and enabling are separate states. Any edit invalidates the previous preflight result. The user must explicitly save, obtain a current successful preflight, and then enable.
- Configuration controls lock while saving, switching state, or running. A running session must always retain a usable Stop control, even if the packaged network core later becomes unavailable.
- Loading and loading failure are explicit page states. A failed load never exposes fabricated editable defaults and always offers Retry.
- Independent mode fails closed: a conflicting broad-capture TUN blocks startup, and failed 4G or SOCKS routes do not silently fall back to the system network.

## Accessibility and copy

- Never communicate health or failure by color alone; pair status color with text.
- Decorative symbols are hidden from accessibility, while icon-only actions receive contextual labels.
- Keep routing names literal and consistent: `4G 直连`, `系统直连`, `系统侧 SOCKS`, `独立分流`, and `Clash 代管`.
- Explain ownership and traffic path in plain language. Avoid marketing language such as acceleration, protection, or global proxying.
