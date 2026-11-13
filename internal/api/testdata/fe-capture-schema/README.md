# FE capture contract (test data)

The Fragments Engine's browser-capture-reader v1 JSON Schemas, copied unchanged from the FE
repository (`contracts/browser-capture-reader/v1/schema`) at the commit in `FE_COMMIT`. They
are identical to the copy fe-clipper vendors. `feschema_test.go` validates every manifest and
completion the delivery worker sends to the stub FE against them, with
`github.com/google/jsonschema-go` (the library and version FE uses). To update, copy the
directory again from FE and change `FE_COMMIT`.

`valid-reader-video.json` and `valid-reader-non-web.json` are FE's own reader-item fixtures from
the same commit (`contracts/browser-capture-reader/v1/fixtures`); `frag_cite_test.go` serves them
(with a real-format fragment id) to check the fragment chip reads FE's reader response correctly.
