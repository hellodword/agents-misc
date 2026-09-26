# SQLite Connection Defaults

Apply these defaults when establishing a SQLite data layer or adding, changing, or diagnosing SQLite connection configuration. Confirm that the project uses SQLite and preserve its established settings.

For connections without an established project policy:

- Enable `PRAGMA foreign_keys = ON` on each connection.
- Use WAL journal mode when the database and target filesystem support it.
- Set a 5000 ms busy timeout on each connection.
- For a concurrent service, default maximum and idle connection counts to 4.
- For a synchronous Rust CLI or desktop process using rusqlite, default to one connection.

Apply only the defaults relevant to the current connection work. Query-only work does not require reconfiguring connections.
