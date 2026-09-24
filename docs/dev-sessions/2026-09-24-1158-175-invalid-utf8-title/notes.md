# Notes

Issue: https://github.com/lmorchard/wideboi/issues/175
Worktree: .worktrees/frame-too-large (branch issue-175-invalid-utf8-title)

## Execution notes

- Phase 3: many CJK characters contain byte 0x9C too (本 = E6 9C AC), not just dingbats like ✳. An "identity" test input with 日本 turned out to be a repair case. Added 日本語 as an integration case.
- Phase 3: the OSC 8 hyperlink test as planned could not fail, because ✳ was the last URL character, so nothing trailed after the false ST. Moved it mid-URL (`/✳/page`); now red under the break check.
- Copilot overview: "can swallow 0x9C in malformed UTF-8 sequences". It was real, though not quite as described. A malformed sequence was released raw, so the parser still treated its 0x9C as ST, but the scanner stayed in OSC state. A later ✳ in plain text then became U+FFFD. Fix: strict second-byte ranges (E0/ED/F0/F4) and a return to ground when released bytes contained 0x9C. Identity test cases added.
- Rebased over #177 (LESSONS.md condensed); re-added both lessons in the new style.
