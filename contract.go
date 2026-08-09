package dedaocli

import (
	_ "embed"
	"encoding/json"
)

//go:embed contract/contract.json
var canonicalContractJSON []byte

var canonicalExitCodeMeanings = loadExitCodeMeanings()

// ExitCodeMeanings returns the canonical process-exit table from contract.json.
func ExitCodeMeanings() map[string]string {
	meanings := make(map[string]string, len(canonicalExitCodeMeanings))
	for code, meaning := range canonicalExitCodeMeanings {
		meanings[code] = meaning
	}
	return meanings
}

func loadExitCodeMeanings() map[string]string {
	var contract struct {
		ExitCodes struct {
			Table map[string]string `json:"table"`
		} `json:"exit_codes"`
	}
	if err := json.Unmarshal(canonicalContractJSON, &contract); err != nil {
		panic("parse embedded contract.json: " + err.Error())
	}
	if len(contract.ExitCodes.Table) == 0 {
		panic("embedded contract.json has no exit-code table")
	}
	return contract.ExitCodes.Table
}
