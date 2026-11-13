# cite/core

Reference detection and resolution, lifted from Tachyon's console
(`plugins/console/ui/src/refs`, MIT, same owner) with no changes to behaviour: the kind
registry, the scanner that splits text into plain and reference segments, the batching
resolver cache, the `tach:` link parser, and the kinds for URLs, Tesseract, Torque, Tether
and file paths. Framework-free TypeScript with no dependencies.

The syntax it recognises is the one the Tether skill `cite-references` teaches agents:
`tesseract:<item_id>`, `tesseract:<namespace>#<key>`, `[label](tach:<service>/<id>)`, and
bare Torque ids. Keep the two in step.

Basalisk's own parts (resolvers, chips, cards, markdown, the terminal link provider) are one
directory up.
