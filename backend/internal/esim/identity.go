package esim

import (
	"fmt"
	"strconv"
	"strings"
)

// CardIdentity 描述面向用户展示的实体 eSIM 产品身份。
// Manufacturer 仍由 PKI/EUM 数据独立识别；这里的 Brand/Model 只在证据足够时填充。
type CardIdentity struct {
	Brand            string                 `json:"brand,omitempty"`
	Model            string                 `json:"model,omitempty"`
	HardwareRevision string                 `json:"hardware_revision,omitempty"`
	Source           string                 `json:"source,omitempty"`
	Confidence       string                 `json:"confidence,omitempty"`
	RuleID           string                 `json:"rule_id,omitempty"`
	SourceURL        string                 `json:"source_url,omitempty"`
	SourceVersion    string                 `json:"source_version,omitempty"`
	Evidence         []CardIdentityEvidence `json:"evidence,omitempty"`
}

type CardIdentityEvidence struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Version string `json:"version"`
}

func cloneCardIdentity(identity *CardIdentity) *CardIdentity {
	if identity == nil {
		return nil
	}
	cloned := *identity
	if len(identity.Evidence) > 0 {
		cloned.Evidence = append([]CardIdentityEvidence(nil), identity.Evidence...)
	}
	return &cloned
}

const (
	identitySourceCardPrivate     = "card_private_aid"
	identitySourceEIDFirmware     = "eid_firmware_rule"
	identityConfidenceConfirmed   = "confirmed"
	identityConfidenceHigh        = "high"
	identityConfidenceMedium      = "medium"
	nekokoVendorsSourceVersion    = "cb5cd194723102220a28767fef1e0742f1cc6c06"
	nekoko9eSIMSourceURL          = "https://github.com/NekokoLPA/Vendors/blob/cb5cd194723102220a28767fef1e0742f1cc6c06/cards/9esim.yaml"
	openEUICC9eSIMSourceVersion   = "8bfff10fc62cdcea0adc71ddbe661838f841d2ac"
	openEUICC9eSIMSourceURL       = "https://gitea.angry.im/PeterCxy/OpenEUICC/src/commit/8bfff10fc62cdcea0adc71ddbe661838f841d2ac/app-common/src/main/java/im/angry/openeuicc/util/Vendors.kt"
	identityRule9eSIMLegacy       = "9esim-kigen-legacy-prefix-firmware-v1"
	identityRule9eSIMV3Sample     = "9esim-kigen-v3-sample-prefix-firmware-v1"
	identityRule9eSIMKigenFamily  = "9esim-kigen-family-firmware-v1"
	identityRulePrivateProductAID = "card-private-product-aid-v1"
)

var legacy9eSIMPrefixes = []string{
	"890440458467274948",
	"890440452167274948",
}

func nineESIMEvidence() []CardIdentityEvidence {
	return []CardIdentityEvidence{
		{
			Kind:    "eid_family",
			Name:    "NekokoLPA/Vendors",
			URL:     nekoko9eSIMSourceURL,
			Version: nekokoVendorsSourceVersion,
		},
		{
			Kind:    "firmware_rule",
			Name:    "OpenEUICC Vendors.kt",
			URL:     openEUICC9eSIMSourceURL,
			Version: openEUICC9eSIMSourceVersion,
		},
	}
}

// 8904404593 是已验证的 9eSIM v3 实卡样本前缀；它不是厂商公布的完整分配范围。
const verified9eSIMV3SamplePrefix = "8904404593"

func privateCardIdentity(model string) *CardIdentity {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	brand := ""
	if strings.Contains(strings.ToLower(model), "estk") {
		brand = "eSTK.me"
	}
	return &CardIdentity{
		Brand:      brand,
		Model:      model,
		Source:     identitySourceCardPrivate,
		Confidence: identityConfidenceConfirmed,
		RuleID:     identityRulePrivateProductAID,
	}
}

func predictCardIdentity(eid, firmware string) *CardIdentity {
	eid = strings.TrimSpace(eid)
	firmware = strings.TrimSpace(firmware)
	if eid == "" || firmware == "" {
		return nil
	}

	isLegacyRange := false
	for _, prefix := range legacy9eSIMPrefixes {
		if strings.HasPrefix(eid, prefix) {
			isLegacyRange = true
			break
		}
	}

	model, revision := identify9eSIMFirmware(firmware)
	if model == "" && isLegacyRange {
		model, revision = identifyLegacy9eSIMFirmware(firmware)
	}
	if model == "" {
		return nil
	}

	identity := &CardIdentity{
		Brand:            "9eSIM",
		Model:            model,
		HardwareRevision: revision,
		Source:           identitySourceEIDFirmware,
		Confidence:       identityConfidenceMedium,
		RuleID:           identityRule9eSIMKigenFamily,
		SourceURL:        nekoko9eSIMSourceURL,
		SourceVersion:    nekokoVendorsSourceVersion,
		Evidence:         nineESIMEvidence(),
	}

	if isLegacyRange {
		identity.Confidence = identityConfidenceHigh
		identity.RuleID = identityRule9eSIMLegacy
		identity.SourceURL = openEUICC9eSIMSourceURL
		identity.SourceVersion = openEUICC9eSIMSourceVersion
		return identity
	}
	if strings.HasPrefix(eid, verified9eSIMV3SamplePrefix) {
		identity.Confidence = identityConfidenceHigh
		identity.RuleID = identityRule9eSIMV3Sample
		return identity
	}

	// NekokoLPA/Vendors 将 9eSIM v3/V2S 都列在 Kigen EUM 89044045 下。
	// 该 8 位值属于制造商范围而非 9eSIM 独占号段，因此这里只给中等可信度。
	if strings.HasPrefix(eid, "89044045") {
		return identity
	}
	return nil
}

func identify9eSIMFirmware(firmware string) (model string, hardwareRevision string) {
	parts := strings.Split(strings.TrimSpace(firmware), ".")
	if len(parts) != 3 {
		return "", ""
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	patch, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return "", ""
	}

	switch {
	case major > 37 || (major == 37 && minor >= 4):
		return "9eSIM v3", "v3.2"
	case major == 37 && minor == 1 && patch >= 41:
		return "9eSIM v3", "v3.1"
	case major == 36 && minor >= 18:
		return "9eSIM v3", "v3"
	case major == 36 && minor == 17 && patch >= 39:
		return "9eSIM v3", "beta"
	case major == 36 && minor == 17 && patch >= 4:
		return "9eSIM V2S", "v2s"
	case major == 36 && minor >= 9:
		return "9eSIM v2.1", "v2.1"
	case major == 36 && minor >= 7:
		return "9eSIM v2", "v2"
	default:
		return "", ""
	}
}

func identifyLegacy9eSIMFirmware(firmware string) (model string, hardwareRevision string) {
	parts := strings.Split(strings.TrimSpace(firmware), ".")
	if len(parts) != 3 {
		return "", ""
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 1 || major >= 36 {
		return "", ""
	}
	return "9eSIM", fmt.Sprintf("firmware %d.x", major)
}
