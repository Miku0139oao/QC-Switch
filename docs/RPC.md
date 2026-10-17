# Management contract v1

Go executable: `qc-switch-core.exe --data-dir <directory>`.
UTF-8 JSON-RPC 2.0, one JSON object per newline over stdin/stdout.
stdout is reserved for the protocol; stderr is diagnostic only. No management
HTTP listener is created. Only one core instance may own a data directory.

Request example:

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
```

`initialize` returns `contract: 1`, application version, settings, public profiles,
client status, gateway state, recent usage, recovery notices, and data directory.
Public profiles expose `hasKey`, never their saved key. Optional state change
notification: `{"jsonrpc":"2.0","method":"state.changed","params":{}}`.
Clients debounce notifications and refresh via `state.get`.

| Method | Parameters | Result |
| --- | --- | --- |
| initialize / state.get | `{}` | Current state |
| profiles.save | Profile, optional key; empty key keeps saved key | State |
| profiles.delete | id | State |
| models.fetch | id | Model IDs; discovery does not grant access |
| connection.test | id, model, kind: auth/text/stream/tools | Independent result, upstream tokens if available |
| clients.apply | client: claude/codex, profileId, model, mode: route/direct, directory, roles | State; writes config only after listener is ready for route mode |
| clients.restore | client, optional choices map | State |
| gateway.stop | `{}` | State; restores route-mode clients first |
| settings.save | port, theme: system/light/dark, retentionDays | State |
| records.clear | `{}` | State |
| backup.export | `{}` | Versioned secret-free backup |
| backup.import | Exported backup | State; does not take over configs or start a listener |
| shutdown | `{}` | ok; restores clients, stops listener, exits core |

Profile fields: `id`, `name`, `endpoint`, `protocol` (messages/responses/chat),
`key`, `models`. Model fields: `id`, `name`, `tools`, `vision`, `reasoning`,
`context`, `efforts`. Claude role mappings: sonnet, opus, haiku, subagent.

Conflict error code `-32009` includes `data.conflicts` with field, original,
managed, current values (credentials redacted). Retry `clients.restore` with
`choices: {"field.path":"current"}` or `"original"` for every conflicting field.
Other operation failures use `-32000`; invalid JSON/request uses standard RPC
codes. Missing request IDs are ignored; initialization must succeed before
presenting state as ready.

Unexpected stdin EOF triggers best-effort restore, preserving externally modified
conflicting fields. Forced termination leaves encrypted journals for next-start
recovery. Neither shutdown path kills a user client process.
