package backend

import (
	"context"
)

// ============================================================================
// OperatorSelectionProvider 实现
// ============================================================================

func (a *ATBackend) ScanOperators(ctx context.Context) ([]OperatorCandidate, error) {
	entries, err := a.modem.ScanOperators()
	if err != nil {
		return nil, err
	}

	var candidates []OperatorCandidate
	for _, entry := range entries {
		var status string
		switch entry.Status {
		case 1:
			status = "available"
		case 2:
			status = "current"
		case 3:
			status = "forbidden"
		default:
			status = "unknown"
		}

		rat := atActToRAT(entry.Act)
		var rats []OperatorAccessTechnology
		if rat != OperatorRATUnknown {
			rats = []OperatorAccessTechnology{rat}
		}

		candidate := OperatorCandidate{
			PLMN:         entry.PLMN,
			OperatorName: entry.LongName,
			ShortName:    entry.ShortName,
			Status:       status,
			RATs:         rats,
		}

		// Normalize PLMN (to get MCC, MNC, etc.)
		normalized, err := NormalizePLMNSelection(entry.PLMN)
		if err == nil {
			candidate.MCC = normalized.MCC
			candidate.MNC = normalized.MNC
			candidate.MNCLength = normalized.MNCLength
			candidate.IncludesPCSDigit = normalized.IncludesPCSDigit
		}

		candidates = append(candidates, candidate)
	}

	return candidates, nil
}

func (a *ATBackend) GetOperatorSelection(ctx context.Context) (OperatorSelection, error) {
	state, err := a.modem.QueryOperatorSelection()
	if err != nil {
		return OperatorSelection{}, err
	}

	sel := OperatorSelection{}
	if state.Mode == 0 {
		sel.Mode = OperatorSelectionAutomatic
	} else if state.Mode == 1 {
		sel.Mode = OperatorSelectionManual
		sel.PLMN = state.PLMN
		sel.RAT = atActToRAT(state.Act)

		normalized, err := NormalizePLMNSelection(state.PLMN)
		if err == nil {
			sel.MCC = normalized.MCC
			sel.MNC = normalized.MNC
			sel.MNCLength = normalized.MNCLength
			sel.IncludesPCSDigit = normalized.IncludesPCSDigit
		}
	} else {
		// Other modes like manual/automatic fallback
		sel.Mode = OperatorSelectionManual
		sel.PLMN = state.PLMN
	}

	return sel, nil
}

func (a *ATBackend) SetOperatorSelection(ctx context.Context, req SetOperatorSelectionRequest) (OperatorSelection, error) {
	if err := ValidateSetOperatorSelectionRequest(req); err != nil {
		return OperatorSelection{}, err
	}

	if req.Mode == OperatorSelectionAutomatic {
		if err := a.modem.SetOperatorAutomatic(); err != nil {
			return OperatorSelection{}, err
		}
		return OperatorSelection{Mode: OperatorSelectionAutomatic}, nil
	}

	act := ratToATAct(req.RAT)
	hasAct := req.RAT != OperatorRATUnknown
	if err := a.modem.SetOperatorManual(req.PLMN, act, hasAct); err != nil {
		return OperatorSelection{}, err
	}

	return a.GetOperatorSelection(ctx)
}

func atActToRAT(act int) OperatorAccessTechnology {
	switch act {
	case 0:
		return OperatorRATGSM
	case 2:
		return OperatorRATWCDMA
	case 7:
		return OperatorRATLTE
	case 10:
		return OperatorRATNR5G
	default:
		return OperatorRATUnknown
	}
}

func ratToATAct(rat OperatorAccessTechnology) int {
	switch rat {
	case OperatorRATGSM:
		return 0
	case OperatorRATWCDMA:
		return 2
	case OperatorRATLTE:
		return 7
	case OperatorRATNR5G:
		return 10
	default:
		return 0
	}
}
