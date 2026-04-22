# ui_public

Runtime assets served by app.py at /earlscheib.

Source of truth: internal/admin/ui/*

These files are a copy. When updating the UI, change internal/admin/ui/
first, then run `make sync-ui` (or manual cp) to refresh this directory.
Both the Go admin binary (embed.go) and this Python server load from
their respective locations — they must stay in sync.
