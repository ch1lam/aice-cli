Improve the structure of the ledger CLI in the current directory without
changing any observable behavior. No new requirement comes with this step.

Make it clear where argument handling, CSV decoding and the money and summary
rules live, and how data flows between them. Keep the design proportionate to
this small program: add an abstraction only when it isolates a real concept.
If the structure is already sound, say so and change only what is worth
changing. The existing tests must still pass.

Finish with a short explanation of the resulting responsibilities and why any
new abstraction is needed.
