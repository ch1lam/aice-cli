Improve the structure of the notes service in the current directory without
changing any observable behavior. No new requirement comes with this step.

Make it clear who owns mutable state, how request handlers reach it, and where
validation lives. Keep the design proportionate to this small program: add an
abstraction only when it isolates a real concept. Merely moving code between
files does not count. If the structure is already sound, say so and change
only what is worth changing. The existing tests must still pass.

Finish with a short explanation of the resulting ownership and why any new
abstraction is needed.
