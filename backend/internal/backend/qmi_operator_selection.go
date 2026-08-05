package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
)

// ============================================================================
// OperatorSelectionProvider 实现
// ============================================================================

func (q *QMIBackend) ScanOperators(ctx context.Context) ([]OperatorCandidate, error) {
	src, err := q.nasSource()
	if err != nil {
		return nil, err
	}

	results, err := src.NASPerformNetworkScan(ctx)
	if err != nil {
		return nil, err
	}
	return qmiNetworkScanResultsToOperatorCandidates(results), nil
}

func (q *QMIBackend) IncrementalOperatorScanSnapshot() ([]OperatorCandidate, bool, time.Time, bool) {
	src, err := q.nasSource()
	if err != nil {
		return nil, false, time.Time{}, false
	}
	info, ts, ok := src.NASIncrementalNetworkScanSnapshot()
	if !ok || info == nil {
		return nil, false, time.Time{}, false
	}
	return qmiNetworkScanResultsToOperatorCandidates(info.Results), info.ScanComplete, ts, true
}

func qmiNetworkScanResultsToOperatorCandidates(results []qmi.NetworkScanResult) []OperatorCandidate {
	candidates := make([]OperatorCandidate, 0, len(results))
	for _, res := range results {
		var status string
		switch res.Status {
		case 1:
			status = "current"
		case 2:
			status = "available"
		case 3:
			status = "forbidden"
		default:
			status = "unknown"
		}

		var rats []OperatorAccessTechnology
		for _, r := range res.RATs {
			rat := qmiRATToOperatorRAT(r)
			if rat != OperatorRATUnknown {
				rats = append(rats, rat)
			}
		}

		candidate := OperatorCandidate{
			PLMN:         fmt.Sprintf("%s%s", res.MCC, res.MNC),
			OperatorName: res.Description,
			Status:       status,
			RATs:         rats,
		}
		if normalized, err := NormalizePLMNSelection(candidate.PLMN); err == nil {
			candidate.MCC = normalized.MCC
			candidate.MNC = normalized.MNC
			candidate.MNCLength = normalized.MNCLength
			candidate.IncludesPCSDigit = normalized.IncludesPCSDigit
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func (q *QMIBackend) GetOperatorSelection(ctx context.Context) (OperatorSelection, error) {
	src, err := q.nasSource()
	if err != nil {
		return OperatorSelection{}, err
	}

	pref, err := src.NASGetSystemSelectionPreference(ctx)
	if err != nil {
		return OperatorSelection{}, err
	}

	sel := OperatorSelection{}
	if pref.HasNetworkSelectionPreference && pref.NetworkSelectionPreference == qmi.NASNetworkSelectionAutomatic {
		sel.Mode = OperatorSelectionAutomatic
	} else if pref.HasManualNetworkSelection {
		sel.Mode = OperatorSelectionManual
		mccStr := fmt.Sprintf("%03d", pref.ManualNetworkSelection.MCC)
		mncFormat := "%02d"
		if pref.ManualNetworkSelection.IncludesPCSDigit {
			mncFormat = "%03d"
		}
		mncStr := fmt.Sprintf(mncFormat, pref.ManualNetworkSelection.MNC)
		sel.PLMN = mccStr + mncStr

		normalized, err := NormalizePLMNSelection(sel.PLMN)
		if err == nil {
			sel.MCC = normalized.MCC
			sel.MNC = normalized.MNC
			sel.MNCLength = normalized.MNCLength
			sel.IncludesPCSDigit = normalized.IncludesPCSDigit
		}
	} else {
		sel.Mode = OperatorSelectionAutomatic
	}

	return sel, nil
}

func (q *QMIBackend) SetOperatorSelection(ctx context.Context, req SetOperatorSelectionRequest) (OperatorSelection, error) {
	sel, err := NormalizeSetOperatorSelectionRequest(req)
	if err != nil {
		return OperatorSelection{}, err
	}

	if req.Mode == OperatorSelectionAutomatic {
		src, err := q.nasSource()
		if err != nil {
			return OperatorSelection{}, err
		}

		pref := qmi.SystemSelectionPreference{
			NetworkSelectionPreference:    qmi.NASNetworkSelectionAutomatic,
			HasNetworkSelectionPreference: true,
			ChangeDuration:                qmi.NASChangeDurationPermanent,
			HasChangeDuration:             true,
		}
		if err := src.NASSetSystemSelectionPreference(ctx, pref); err != nil {
			return OperatorSelection{}, err
		}

		if err := q.NASInitiateNetworkRegister(ctx, NASRegisterRequest{Mode: "automatic"}); err != nil {
			return OperatorSelection{}, err
		}
		return q.GetOperatorSelection(ctx)
	}

	regReq, err := BuildManualNASRegisterRequest(sel)
	if err != nil {
		return OperatorSelection{}, err
	}
	if err := q.NASInitiateNetworkRegister(ctx, regReq); err != nil {
		return OperatorSelection{}, err
	}
	return sel, nil
}

func qmiRATToOperatorRAT(rat uint8) OperatorAccessTechnology {
	switch rat {
	case 0x04:
		return OperatorRATGSM
	case 0x05, 0x09:
		return OperatorRATWCDMA
	case 0x08:
		return OperatorRATLTE
	case 0x0C:
		return OperatorRATNR5G
	default:
		return OperatorRATUnknown
	}
}
