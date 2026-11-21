Search the tools this session holds back, and reveal the ones you need.

Lazy tool mode sends only a small core palette on every request to keep the tool-schema cost down: read/view, edit, bash, grep, glob, ls, todos, the delegation tools, and this one. Every other tool exists and works, but its schema is not sent until you reveal it here.

Give a tool name when you know it, or words that describe what you want to do ("language server references", "fetch a URL", "open a GitHub issue"). The reply lists the matching tools with their full parameter schemas, and those tools are callable from your next step for the rest of the session.

A tool you have not revealed cannot be called: the request is rejected as an unknown tool. Search once for each tool you need, then use it directly. Repeating a search for a tool you already revealed only costs a round trip.
