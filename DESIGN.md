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
- The two modes are mutually exclusive:
  - `独立分流` owns the single TUN and application rules. `4G 直连` never uses SOCKS; unlisted applications use the system default network.
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
