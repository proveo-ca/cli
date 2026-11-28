

Live verification (required, whatever the template above says):
- Before judging, read every AGENTS.md that applies: the repository root, the folders the change touches, and tooling/AGENTS.md when it exists. Follow their setup to run the app in this sandbox: dependencies, codegen, the Docker stack, the database and its migrations, the web and AI servers.
- Verify each behavioural claim, from the PR, its ticket or your own findings, on the live app. Drive the host Chrome with `agent-browser`; proveo points it at host Chrome over CDP, and `agent-browser skills get core` shows how. Say how you checked each claim: the command and its output, the URL and what the page showed, or the rows a sync wrote.
- Open the report with an Access table, before the findings: integration | check you ran | reachable? | the exact error. Cover the Docker daemon, the app's health URL, host Chrome over CDP, every vendor or API the change calls, each MCP server you needed (Datadog, Linear), and GitHub. Never skip an integration silently.
- A claim you could not check because an integration was unreachable is labelled [Unverified: <integration> unreachable] and stays unjudged.
- Running the app is part of the review; it stays read-only otherwise. No commits, pushes, posted comments or edits to tracked files, and no edits to the shared .env: report a missing value in the Access table instead.
