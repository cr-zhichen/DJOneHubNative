package modem

import "strings"

var servingOperatorNameByPLMN = map[string]string{
	// 中国大陆
	"46000": "中国移动",
	"46002": "中国移动",
	"46004": "中国移动",
	"46007": "中国移动",
	"46008": "中国移动",
	"46013": "中国移动",
	"46001": "中国联通",
	"46006": "中国联通",
	"46009": "中国联通",
	"46003": "中国电信",
	"46005": "中国电信",
	"46011": "中国电信",
	"46015": "中国广电",

	// 中国香港
	"45400": "CSL",
	"45402": "CSL",
	"45410": "CSL",
	"45418": "CSL",
	"45403": "电讯盈科",
	"45416": "电讯盈科",
	"45419": "电讯盈科",
	"45404": "3 HK",
	"45406": "数码通",
	"45415": "数码通",
	"45407": "中国移动香港",
	"45412": "中国联通香港",

	// 中国台湾
	"46601": "远传电信",
	"46602": "远传电信",
	"46605": "亚太电信",
	"46689": "台湾之星",
	"46692": "中华电信",
	"46693": "台湾大哥大",
	"46697": "台湾大哥大",
	"46699": "台湾大哥大",
}

func normalizeOperatorCode(code string) string {
	code = strings.TrimSpace(code)
	return strings.Trim(code, "\"")
}

// LookupServingOperatorNameFromPLMN returns the mapped serving-network display name when the PLMN is known.
func LookupServingOperatorNameFromPLMN(plmn string) (string, bool) {
	plmn = normalizeOperatorCode(plmn)
	if plmn == "" {
		return "", false
	}
	name, ok := servingOperatorNameByPLMN[plmn]
	return name, ok
}

// ResolveServingOperatorNameFromPLMN returns a serving-network display name when known, otherwise the normalized raw PLMN.
func ResolveServingOperatorNameFromPLMN(plmn string) string {
	plmn = normalizeOperatorCode(plmn)
	if plmn == "" {
		return ""
	}
	if name, ok := LookupServingOperatorNameFromPLMN(plmn); ok {
		return name
	}
	return plmn
}
