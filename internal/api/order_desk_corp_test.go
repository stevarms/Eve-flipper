package api

import (
	"errors"
	"strings"
	"testing"

	"eve-flipper/internal/corp"
	"eve-flipper/internal/esi"
)

// fakeCorpOrderSource stands in for ESI. Every method answers from a map, so a
// test can say "this character is in that corp, holds these roles, and the corp
// book is this" without a token.
type fakeCorpOrderSource struct {
	corpByCharacter  map[int64]int32
	corpIDErr        map[int64]error
	rolesByCharacter map[int64][]string
	rolesErr         map[int64]error
	ordersByCorp     map[int32][]corp.CorpMarketOrder
	ordersErr        map[int32]error
	names            map[int32]string

	// ordersCalls records (corpID, viaCharacterID) per fetch so a test can
	// assert the book was asked for exactly once and through whom.
	ordersCalls []corpFetchCall
}

type corpFetchCall struct {
	corpID          int32
	viaCharacterID  int64
	viaCharacterTok string
}

func (f *fakeCorpOrderSource) CorporationIDFor(characterID int64) (int32, error) {
	if err, ok := f.corpIDErr[characterID]; ok {
		return 0, err
	}
	return f.corpByCharacter[characterID], nil
}

func (f *fakeCorpOrderSource) RolesFor(characterID int64, token string) ([]string, error) {
	if err, ok := f.rolesErr[characterID]; ok {
		return nil, err
	}
	return f.rolesByCharacter[characterID], nil
}

func (f *fakeCorpOrderSource) OrdersFor(corpID int32, viaCharacterID int64, token string) ([]corp.CorpMarketOrder, error) {
	f.ordersCalls = append(f.ordersCalls, corpFetchCall{corpID: corpID, viaCharacterID: viaCharacterID, viaCharacterTok: token})
	if err, ok := f.ordersErr[corpID]; ok {
		return nil, err
	}
	return f.ordersByCorp[corpID], nil
}

func (f *fakeCorpOrderSource) CorporationName(corpID int32) string {
	if n, ok := f.names[corpID]; ok {
		return n
	}
	return "Corporation"
}

const (
	deskJitaAltID   = int64(90000001)
	deskFWPilotID   = int64(90000002)
	deskCorpID      = int32(98000001)
	deskOnnamonIV   = int64(60015070)
	deskBlackRiseID = int32(10000069)
	typeScourgeLM   = int32(210)
)

func deskSession(id int64, name string) deskCorpSession {
	return deskCorpSession{characterID: id, characterName: name, token: "tok-" + name}
}

// A corp order must arrive tagged as the corporation's, and must name the
// character whose fee rates its broker fee and sales tax actually follow --
// which is the issuer, not the character the book was fetched through.
func TestCorpOrderIsTaggedAndNamesItsFeeCharacter(t *testing.T) {
	src := &fakeCorpOrderSource{
		corpByCharacter:  map[int64]int32{deskJitaAltID: deskCorpID, deskFWPilotID: deskCorpID},
		rolesByCharacter: map[int64][]string{deskJitaAltID: {"Trader"}, deskFWPilotID: {"Hangar_Query_1"}},
		names:            map[int32]string{deskCorpID: "Onnamon Logistics"},
		ordersByCorp: map[int32][]corp.CorpMarketOrder{deskCorpID: {{
			OrderID:       7001,
			CharacterID:   deskFWPilotID,
			CharacterName: "FW Pilot",
			TypeID:        typeScourgeLM,
			TypeName:      "Scourge Light Missile",
			Price:         120.5,
			VolumeRemain:  4000,
			VolumeTotal:   5000,
			LocationID:    deskOnnamonIV,
			RegionID:      deskBlackRiseID,
			Duration:      90,
			Range:         "station",
		}}},
	}

	orders, owners, warnings := collectCorpDeskOrders(
		[]deskCorpSession{deskSession(deskJitaAltID, "Jita Alt"), deskSession(deskFWPilotID, "FW Pilot")}, src)

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	if len(orders) != 1 {
		t.Fatalf("expected 1 corp order, got %d", len(orders))
	}
	if orders[0].OrderID != 7001 || orders[0].TypeID != typeScourgeLM || orders[0].LocationID != deskOnnamonIV {
		t.Fatalf("order did not survive the shaping: %+v", orders[0])
	}
	// Range is what lets the desk judge a corp buy order against the bids that
	// can actually reach its station.
	if orders[0].Range != "station" {
		t.Fatalf("range not carried: %q", orders[0].Range)
	}
	if orders[0].VolumeRemain != 4000 || orders[0].VolumeTotal != 5000 || orders[0].Duration != 90 {
		t.Fatalf("quantities or duration lost: %+v", orders[0])
	}

	owner, ok := owners[7001]
	if !ok {
		t.Fatal("corp order carries no owner tag")
	}
	if owner.kind != orderOwnerKindCorporation {
		t.Fatalf("owner kind = %q, want %q", owner.kind, orderOwnerKindCorporation)
	}
	if owner.id != int64(deskCorpID) || owner.name != "Onnamon Logistics" {
		t.Fatalf("owner is not the corporation: %+v", owner)
	}
	// The book was fetched through the Jita alt (the role holder), but the
	// order was issued by the FW pilot, and it is the FW pilot's Accounting and
	// Broker Relations that set its fees.
	if owner.feeCharacterID != deskFWPilotID || owner.feeCharacterName != "FW Pilot" {
		t.Fatalf("fee character = %d/%q, want %d/FW Pilot",
			owner.feeCharacterID, owner.feeCharacterName, deskFWPilotID)
	}
	if len(src.ordersCalls) != 1 {
		t.Fatalf("expected exactly one corp book fetch, got %d", len(src.ordersCalls))
	}
	if src.ordersCalls[0].viaCharacterID != deskJitaAltID {
		t.Fatalf("fetched through %d, want the role holder %d", src.ordersCalls[0].viaCharacterID, deskJitaAltID)
	}
}

// An unresolved issuer falls back to the character the book came through, so a
// corp row never shows a blank fee owner.
func TestCorpOrderWithoutIssuerFallsBackToTheFetchingCharacter(t *testing.T) {
	src := &fakeCorpOrderSource{
		corpByCharacter:  map[int64]int32{deskJitaAltID: deskCorpID},
		rolesByCharacter: map[int64][]string{deskJitaAltID: {"Director"}},
		ordersByCorp:     map[int32][]corp.CorpMarketOrder{deskCorpID: {{OrderID: 7002, TypeID: typeScourgeLM}}},
	}
	_, owners, _ := collectCorpDeskOrders([]deskCorpSession{deskSession(deskJitaAltID, "Jita Alt")}, src)
	owner := owners[7002]
	if owner.feeCharacterID != deskJitaAltID || owner.feeCharacterName != "Jita Alt" {
		t.Fatalf("fee character = %d/%q, want the fetching character", owner.feeCharacterID, owner.feeCharacterName)
	}
}

// A corp nobody can read must produce a warning naming it. Asserted as a
// warning string, not a row count: zero rows is exactly what the silent failure
// also looks like, and reading it as "no competition" is how the tool would lie.
func TestCorpWithNoRoleHoldingSessionWarnsRatherThanReturningNothing(t *testing.T) {
	src := &fakeCorpOrderSource{
		corpByCharacter:  map[int64]int32{deskJitaAltID: deskCorpID},
		rolesByCharacter: map[int64][]string{deskJitaAltID: {"Hangar_Take_1", "Rent_Office"}},
		names:            map[int32]string{deskCorpID: "Onnamon Logistics"},
		ordersByCorp:     map[int32][]corp.CorpMarketOrder{deskCorpID: {{OrderID: 7003}}},
	}
	orders, _, warnings := collectCorpDeskOrders([]deskCorpSession{deskSession(deskJitaAltID, "Jita Alt")}, src)

	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning, got %v", warnings)
	}
	if !strings.Contains(warnings[0], "Onnamon Logistics") {
		t.Fatalf("warning does not name the corporation: %q", warnings[0])
	}
	if !strings.Contains(warnings[0], "Trader") {
		t.Fatalf("warning does not say which role is missing: %q", warnings[0])
	}
	if len(orders) != 0 {
		t.Fatalf("orders were fetched without a role: %d", len(orders))
	}
	if len(src.ordersCalls) != 0 {
		t.Fatal("corp book was fetched despite no role holder")
	}
}

// The role question is about the corporation, not about every character in it.
// One role holder is enough and the others must not each generate a warning.
func TestOneRoleHolderIsEnoughAndTheRestAreSilent(t *testing.T) {
	src := &fakeCorpOrderSource{
		corpByCharacter: map[int64]int32{deskJitaAltID: deskCorpID, deskFWPilotID: deskCorpID},
		// The first session listed cannot read the book; the second can.
		rolesByCharacter: map[int64][]string{deskJitaAltID: {"Hangar_Take_1"}, deskFWPilotID: {"Accountant"}},
		ordersByCorp:     map[int32][]corp.CorpMarketOrder{deskCorpID: {{OrderID: 7004, TypeID: typeScourgeLM}}},
	}
	orders, owners, warnings := collectCorpDeskOrders(
		[]deskCorpSession{deskSession(deskJitaAltID, "Jita Alt"), deskSession(deskFWPilotID, "FW Pilot")}, src)

	if len(warnings) != 0 {
		t.Fatalf("a reachable corporation warned anyway: %v", warnings)
	}
	if len(orders) != 1 || owners[7004].kind != orderOwnerKindCorporation {
		t.Fatalf("orders=%d owner=%+v", len(orders), owners[7004])
	}
	if len(src.ordersCalls) != 1 || src.ordersCalls[0].viaCharacterID != deskFWPilotID {
		t.Fatalf("book not fetched once through the role holder: %+v", src.ordersCalls)
	}
}

// A missing market scope and a missing corp role want different fixes, so they
// must not share a warning.
func TestCorpScopeAndRoleFailuresReadDifferently(t *testing.T) {
	scopeSrc := &fakeCorpOrderSource{
		corpByCharacter:  map[int64]int32{deskJitaAltID: deskCorpID},
		rolesByCharacter: map[int64][]string{deskJitaAltID: {"Trader"}},
		ordersErr:        map[int32]error{deskCorpID: errors.New("corp orders: ESI 403 Forbidden")},
	}
	_, _, warnings := collectCorpDeskOrders([]deskCorpSession{deskSession(deskJitaAltID, "Jita Alt")}, scopeSrc)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "read_corporation_orders") {
		t.Fatalf("403 did not name the market scope: %v", warnings)
	}

	roleReadSrc := &fakeCorpOrderSource{
		corpByCharacter: map[int64]int32{deskJitaAltID: deskCorpID},
		rolesErr:        map[int64]error{deskJitaAltID: errors.New("character roles: ESI 403 Forbidden")},
	}
	_, _, warnings = collectCorpDeskOrders([]deskCorpSession{deskSession(deskJitaAltID, "Jita Alt")}, roleReadSrc)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "read_corporation_roles") {
		t.Fatalf("unreadable roles did not name the roles scope: %v", warnings)
	}
}

// Merging the corp book into the character book must not double anything: not
// the rows, not the owners, and not the (region, type) book fetches the desk
// then fans out over.
func TestCharacterAndCorpOrdersForTheSameTypeAreNotDoubleCounted(t *testing.T) {
	src := &fakeCorpOrderSource{
		corpByCharacter:  map[int64]int32{deskFWPilotID: deskCorpID},
		rolesByCharacter: map[int64][]string{deskFWPilotID: {"Trader"}},
		ordersByCorp: map[int32][]corp.CorpMarketOrder{deskCorpID: {{
			OrderID:    7006,
			TypeID:     typeScourgeLM,
			LocationID: deskOnnamonIV,
			RegionID:   deskBlackRiseID,
			Price:      118,
		}}},
	}
	corpOrders, corpOwners, _ := collectCorpDeskOrders([]deskCorpSession{deskSession(deskFWPilotID, "FW Pilot")}, src)

	// The character's own order for the same type at the same station.
	orders := []esi.CharacterOrder{{
		OrderID:    7005,
		TypeID:     typeScourgeLM,
		LocationID: deskOnnamonIV,
		RegionID:   deskBlackRiseID,
		Price:      121,
	}}
	owners := map[int64]orderOwner{7005: {
		kind: orderOwnerKindCharacter, id: deskFWPilotID, name: "FW Pilot",
		feeCharacterID: deskFWPilotID, feeCharacterName: "FW Pilot",
	}}
	orders = append(orders, corpOrders...)
	for id, o := range corpOwners {
		owners[id] = o
	}

	if len(orders) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(orders))
	}
	if len(owners) != 2 {
		t.Fatalf("expected 2 owner tags, got %d -- an order id collided", len(owners))
	}
	if owners[7005].kind != orderOwnerKindCharacter || owners[7006].kind != orderOwnerKindCorporation {
		t.Fatalf("owners crossed: %+v / %+v", owners[7005], owners[7006])
	}

	// This is the shape buildOrderDesk fans out over. Two owners at one station
	// for one type is one book, because the pair set is a map.
	type regionType struct {
		regionID int32
		typeID   int32
	}
	pairs := make(map[regionType]bool)
	for _, o := range orders {
		pairs[regionType{o.RegionID, o.TypeID}] = true
	}
	if len(pairs) != 1 {
		t.Fatalf("expected 1 (region, type) book fetch, got %d", len(pairs))
	}
}

func TestHasCorpMarketOrderRole(t *testing.T) {
	cases := []struct {
		roles []string
		want  bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"Trader"}, true},
		{[]string{"trader"}, true},
		{[]string{" Accountant "}, true},
		{[]string{"Director"}, true},
		{[]string{"Junior_Accountant"}, false}, // reads the wallet, not the orders
		{[]string{"Hangar_Take_1", "Rent_Office"}, false},
		{[]string{"Station_Manager", "TRADER"}, true},
	}
	for _, c := range cases {
		if got := hasCorpMarketOrderRole(c.roles); got != c.want {
			t.Errorf("hasCorpMarketOrderRole(%v) = %v, want %v", c.roles, got, c.want)
		}
	}
}

// A user with no corporation-capable session at all is not a warning: there is
// nothing to report and nothing missing.
func TestNoSessionsMeansNoWarnings(t *testing.T) {
	orders, owners, warnings := collectCorpDeskOrders(nil, &fakeCorpOrderSource{})
	if len(orders) != 0 || len(owners) != 0 || len(warnings) != 0 {
		t.Fatalf("empty input produced %d orders, %d owners, %v", len(orders), len(owners), warnings)
	}
	orders, _, warnings = collectCorpDeskOrders([]deskCorpSession{deskSession(deskJitaAltID, "Jita Alt")}, nil)
	if len(orders) != 0 || len(warnings) != 0 {
		t.Fatalf("nil source produced %d orders, %v", len(orders), warnings)
	}
}
