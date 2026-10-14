Manage MCP servers that are configured but not started.

Some MCP servers are configured with lazy loading: they are enabled, but Crush
only starts them when a turn needs them, so they cost nothing while unused.

Use `action: "list"` to see the servers that are not running yet, and
`action: "activate"` with the server name to start one. Activating a server
makes its tools callable from your next step of the same turn; the result of
this tool lists them. Activation can take a few seconds.

Prefer activating a server over telling the user it is unavailable, and never
guess a server's tools before activating it.
