# Database model conventions

`iris-api/model` is the source of truth for database models shared with other
services. Keep each GORM model in its own file.

## File and type names

- Use one persisted model struct per file.
- Name the file after the table in lowercase `snake_case` (for example,
  `browser_profile.go` for `BrowserProfile` and `browser_profile`).
- Name the Go struct in `PascalCase` from the singular table name.
- Keep model-specific enums, status constants, validation, and custom database
  types alongside their model when they are only used by that model.

## Primary and external IDs

- Use an auto-incrementing `uint` primary key named `Id{Model}`.
- Set both GORM tags explicitly: `primaryKey;autoIncrement;column:id_{model}`.
  For example, `IdBrowserProfile` maps to `id_browser_profile`.
- Models exposed across service/API boundaries should also have an `IdExternal`
  UUID with `gorm:"unique;type:uuid;default:gen_random_uuid()"`. Omit it only
  when the model is strictly internal and has no external identity requirement.
- Foreign-key fields should be explicit ID fields with a
  `column:id_{related_model}` tag; define GORM associations separately. If the
  FK field name would be identical to the referenced model's primary-key field,
  use an unambiguous name such as `BrowserSessionID` so GORM creates the FK on
  the owning table (not a reverse relation on the referenced table).

## Schema and methods

- Define `TableName()` on each model and return its explicit singular
  `snake_case` table name.
- Put model validation and hooks in the same file as the model.
- If a model or one of its model-specific field types needs `sql.Scanner` or
  `driver.Valuer`, implement its `Scan` and `Value` methods in that same model
  file. Do not move those methods into a generic JSON helper file.
- Give persisted JSON a typed Go schema with explicit JSON tags. Avoid
  `json.RawMessage` for fields whose shape is part of the model contract.
- Keep database column names, types, nullability, defaults, indexes, and
  constraints explicit in GORM tags when they are part of the schema.
- Use `CreatedAt` and `UpdatedAt` timestamps where applicable. Add `DeletedAt`
  with an index for models that support soft deletion.

## Cross-service changes

- Define shared models here; other services should use type aliases rather than
  duplicate model structs.
- After changing models, update the API migration registration for the affected
  tables only. Keep unrelated migrations disabled when preparing a migration
  run.
- Regenerate `iris-worker/vendor` with `go mod vendor` from the worker module
  after changing this package, and run relevant model and service tests.
