# Desktop session manager (#123)

The accepted product decisions are in [issue 123](https://github.com/lmorchard/wideboi/issues/123).
The first desktop app manages local sessions on macOS and Linux, with one
window per open session and a manager window available from the application
menu. Desktop-created sessions belong to the app until the user keeps them
running. Quitting with owned sessions offers stop, keep, or cancel.

New sessions start in a chosen project folder or Home. If a session already
started in that folder, the manager offers to open it or create another.

Windows, remote sessions, updates, signing, and notarization are outside this
first implementation.
