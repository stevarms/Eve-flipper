package api

import (
	"testing"
)

func TestHasStockpileCorpAssetRole(t *testing.T) {
	cases := []struct {
		name  string
		roles []string
		want  bool
	}{
		{"director", []string{"Director"}, true},
		{"accountant", []string{"Accountant"}, true},
		{"junior_accountant", []string{"Junior_Accountant"}, true},
		{"trader", []string{"Trader"}, true},
		{"auditor", []string{"Auditor"}, true},
		{"lowercase director", []string{"director"}, true},
		{"mixed set", []string{"Personnel_Manager", "Auditor"}, true},
		{"none accepted", []string{"Personnel_Manager"}, false},
		{"empty", nil, false},
		{"unrelated only", []string{"Fitting_Manager", "Container_Take_1"}, false},
	}
	for _, tc := range cases {
		if got := hasStockpileCorpAssetRole(tc.roles); got != tc.want {
			t.Errorf("case %q: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
