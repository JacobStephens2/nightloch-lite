# Current decision

**As of:** 2026-07-28  
**Plan:** [plan.md](./plan.md)  
**Slice 0 review:** [reviews/slice-0-review.md](./reviews/slice-0-review.md)

---

## Slice 0 complete (+ S1–S9 review fixes)

- DST-safe schedule breakpoints (`AddDate`)
- Multi-TZ property tests + TZ-change-across-DST regression
- `SubjectMayApplyEdit` → `EditResult` (deny-closed on invalid)
- Empty window rejected; GlobalList parent-walk; `/opt/nightloch` deploy paths; CI hardened

```bash
go test ./...
go build -o bin/nightloch ./cmd/nightloch
```

---

## Next

**Slice 1** — OpenTofu + `lite.nightloch.app` (explicit cloud apply approval).
