package modem

import (
	"fmt"
	"time"
)

type OperatorScanEntry struct {
	Status    int
	LongName  string
	ShortName string
	PLMN      string
	Act       int
}

type OperatorSelectionState struct {
	Mode   int
	Format int
	PLMN   string
	Act    int
	HasAct bool
}

func (m *Manager) ScanOperators() ([]OperatorScanEntry, error) {
	resp, err := m.ExecuteAT("AT+COPS=?", 90*time.Second)
	if err != nil {
		return nil, err
	}
	return parseCOPSScan(resp), nil
}

func (m *Manager) QueryOperatorSelection() (OperatorSelectionState, error) {
	resp, err := m.ExecuteAT("AT+COPS?", 5*time.Second)
	if err != nil {
		return OperatorSelectionState{}, err
	}
	state, ok := parseCOPSSelection(resp)
	if !ok {
		return OperatorSelectionState{}, fmt.Errorf("failed to parse cops selection")
	}
	return state, nil
}

func (m *Manager) SetOperatorAutomatic() error {
	_, err := m.ExecuteAT("AT+COPS=0", 30*time.Second)
	return err
}

func (m *Manager) SetOperatorManual(plmn string, act int, hasAct bool) error {
	cmd := fmt.Sprintf(`AT+COPS=1,2,"%s"`, plmn)
	if hasAct {
		cmd = fmt.Sprintf(`AT+COPS=1,2,"%s",%d`, plmn, act)
	}
	_, err := m.ExecuteAT(cmd, 60*time.Second)
	return err
}
