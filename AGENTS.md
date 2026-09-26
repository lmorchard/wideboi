# Agent instructions

Read `CLAUDE.md` and `docs/LESSONS.md` before changing this repository.

Always do task work in a dedicated Git worktree. Create the worktree before
editing files, running a build that writes artifacts, or committing. Never use
the main checkout as a task workspace. Keep existing worktrees and uncommitted
changes untouched.

**NEVER PUSH DIRECTLY TO MAIN.** Always create a branch in your worktree, push the branch, and open a Pull Request for the user to review.

If `gh` authentication or API access fails in the sandbox, retry the GitHub
CLI command with sandbox escalation before using computer/browser automation.

**DO NOT KILL WIDEBOI SERVERS.** The agent itself is being hosted by a `wideboi server` process. Killing stray `wideboi` processes during tests or otherwise may terminate your own connection.

**NEVER LET A TEST REACH A REAL SESSION.** You are probably running inside the
`default` wideboi session. Anything that runs without an explicit socket --
a dispatched command, `config.DefaultSocketPath()`, the auto-cleanup sweep --
resolves to it, and a test that sent `quit` there ended the session hosting
the agent (#277). Give every test an explicit socket in a temp dir, and keep
the `testenv.Run` `TestMain` in any package whose tests dispatch commands or
touch the session directory. If your session vanishes, `exits.log` beside the
session sockets records who ended it (#276).
