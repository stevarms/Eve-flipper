package engine

// Decryptor is the Go mirror of the frontend industryDecryptors table. Each
// entry holds the canonical modifiers a decryptor applies to a T2 invention
// attempt: probability multiplier, output BPC runs bonus, ME/TE delta on the
// invented copy, and a ballpark cost per attempt.
//
// The scanner's per-row auto-optimize loops through this table, running the
// analyzer once per decryptor, and picks the winner (highest ISK/h). Users
// don't have to specify a decryptor up front — the tool tells them which
// one (if any) is most profitable for each invention target.
type Decryptor struct {
	Key             string
	Name            string
	TypeID          int32
	ProbMult        float64 // multiplier on invention probability
	OutputRunsBonus int32   // added to the base T2 BPC runs (default 10)
	MEDelta         int32   // added to the T2 BPC's ME (base 2)
	TEDelta         int32   // added to the T2 BPC's TE (base 4)
	DefaultCost     float64 // ballpark Jita cost per decryptor, ISK
}

// T2BPCBaseME and T2BPCBaseTE are the ME/TE of a freshly-invented T2 BPC
// before decryptor modifiers. Mirrors the frontend constants.
const (
	T2BPCBaseME   = 2
	T2BPCBaseTE   = 4
	T2BPCBaseRuns = 10
)

// Decryptors lists every canonical decryptor + the "None" no-op entry, in
// the standard EVE order. Frontend and backend must stay in lockstep here
// or the auto-picker will produce different results between UI and API.
var Decryptors = []Decryptor{
	{Key: "none", Name: "None", TypeID: 0, ProbMult: 1.0, OutputRunsBonus: 0, MEDelta: 0, TEDelta: 0, DefaultCost: 0},
	{Key: "accelerant", Name: "Accelerant", TypeID: 34201, ProbMult: 1.2, OutputRunsBonus: 1, MEDelta: 2, TEDelta: 10, DefaultCost: 400_000},
	{Key: "attainment", Name: "Attainment", TypeID: 34202, ProbMult: 1.8, OutputRunsBonus: 4, MEDelta: -1, TEDelta: 4, DefaultCost: 550_000},
	{Key: "augmentation", Name: "Augmentation", TypeID: 34203, ProbMult: 0.6, OutputRunsBonus: 9, MEDelta: -2, TEDelta: 2, DefaultCost: 350_000},
	{Key: "optimizedAttainment", Name: "Optimized Attainment", TypeID: 34204, ProbMult: 1.9, OutputRunsBonus: 2, MEDelta: 1, TEDelta: -2, DefaultCost: 800_000},
	{Key: "optimizedAugmentation", Name: "Optimized Augmentation", TypeID: 34205, ProbMult: 0.9, OutputRunsBonus: 7, MEDelta: 2, TEDelta: 0, DefaultCost: 700_000},
	{Key: "parity", Name: "Parity", TypeID: 34206, ProbMult: 1.5, OutputRunsBonus: 3, MEDelta: 1, TEDelta: -2, DefaultCost: 500_000},
	{Key: "process", Name: "Process", TypeID: 34207, ProbMult: 1.1, OutputRunsBonus: 0, MEDelta: 3, TEDelta: 6, DefaultCost: 300_000},
	{Key: "symmetry", Name: "Symmetry", TypeID: 34208, ProbMult: 1.0, OutputRunsBonus: 2, MEDelta: 1, TEDelta: 8, DefaultCost: 300_000},
}

// EffectiveInventionParams returns the ME/TE/output-runs/probability-mult
// values a given decryptor applies. Clamps ME/TE to their legal ranges.
func (d Decryptor) EffectiveInventionParams() (meBase, teBase, outputRuns int32, chanceMult float64, cost float64) {
	meBase = T2BPCBaseME + d.MEDelta
	if meBase < 0 {
		meBase = 0
	}
	if meBase > 10 {
		meBase = 10
	}
	teBase = T2BPCBaseTE + d.TEDelta
	if teBase < 0 {
		teBase = 0
	}
	if teBase > 20 {
		teBase = 20
	}
	outputRuns = T2BPCBaseRuns + d.OutputRunsBonus
	if outputRuns < 1 {
		outputRuns = 1
	}
	chanceMult = d.ProbMult
	if chanceMult <= 0 {
		chanceMult = 1
	}
	cost = d.DefaultCost
	if cost < 0 {
		cost = 0
	}
	return
}
