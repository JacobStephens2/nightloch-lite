# Current decision

**As of:** 2026-07-28  
**Full log:** [DECISIONS.md](./DECISIONS.md)  
**Freeze:** [shared-understanding.md](./shared-understanding.md)  
**Slice −1 design:** [docs/SECURITY-POLICY-DESIGN.md](./docs/SECURITY-POLICY-DESIGN.md)  
**Slice −1 review:** [reviews/slice-neg-1-review.md](./reviews/slice-neg-1-review.md)  
**Plan:** [plan.md](./plan.md)

---

## Slice −1 + review fixes

Slice −1 complete; [slice-neg-1-review](./reviews/slice-neg-1-review.md) **R1–R11** applied:

| ID | Fix |
|---|---|
| R1 | Global lists in P (lazy); allowlist is loosen |
| R2 | Curfew wins via `$important`; allows carve lists only |
| R3 | H=14d requires daily/weekly-only schedules |
| R4 | All device revokes immediate |
| R5 | Log-share off: instant consent exemption (written) |
| R6 | DECISIONS Q11/Q21/Q33/Q38 marked |
| R7 | Precedence headers on design/freeze/decisions/plan |
| R8 | Mutual edges + solo dogfood honesty |
| R9 | v2 `enrollment_allowed` note |
| R10 | Lifecycle decline/expired; notify wording |
| R11 | From `alerts@nightloch.app` |

---

## Next

**Slice 0** — Go module + `internal/policy` property tests against the amended predicate.
