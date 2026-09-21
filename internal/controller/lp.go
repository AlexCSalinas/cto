package controller

// LP is where an optimisation-based rebalance step would go. It currently
// behaves exactly like Greedy so it can be run and compared; only the
// rebalance decision is meant to change.
//
// TODO(lp): replace Greedy.Tick with an ILP solved every tick:
//
//	Decision variables
//	  x[b][h] ∈ {0,1}   box b is on host h after this tick
//	  y[h]    ∈ {0,1}   host h stays alive
//	  m[b]    ∈ {0,1}   box b is migrated (x[b][h] = 1 for some h ≠ current(b))
//
//	Objective (minimise, per tick interval T)
//	  Σ_h y[h]·price[h]·T                          host cost
//	  + λ · Σ_b m[b]·(DirtyGB[b] + floor)/bw[b]     migration cost in seconds
//	  + μ · Σ_b lostRisk[b]                         optional: spot hazard × uncheckpointed work
//
//	Constraints
//	  Σ_h x[b][h] = 1                      ∀ live b   each box on exactly one host
//	  Σ_b x[b][h]·demandMem[b] ≤ y[h]·MemGB[h]·hot  ∀ h  observed+headroom fits
//	  Σ_b x[b][h]·demandCPU[b] ≤ y[h]·CPU[h]       ∀ h
//	  x[b][h] ≤ y[h]                       ∀ b,h      no box on a dead host
//	  m[b] ≥ x[b][h]                       ∀ b, h ≠ current(b)
//	  Σ_b m[b]·[current(b)=h or h] ≤ maxConcurrent   ∀ h  slot limit
//
// With a few hundred boxes and ~20 hosts this is a small ILP; a solver is
// out of scope for a stdlib-only project, which is why this is a stub.
type LP struct{ *Greedy }

// NewLP builds the LP placeholder.
func NewLP(cfg Config) *LP { return &LP{Greedy: NewGreedy(cfg)} }

// Name implements Controller.
func (LP) Name() string { return "lp" }
