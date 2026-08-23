# Protocol Compatibility Policy

The canonical protocol contract is `schema/v1.schema.json`. Its
`protocolVersion` value and major schema filename are versioned together.

Changes within protocol v1 must be backward compatible:

- new fields must be optional;
- existing field names, JSON types, and meanings cannot change;
- required fields cannot be added to existing message types;
- enum values and error codes cannot be removed or renamed;
- existing limits cannot be narrowed;
- clients must ignore unknown optional fields;
- servers must reject unsupported protocol versions before registration.

Adding a required field, changing field semantics, or removing an accepted
shape requires a new major schema and a new `protocolVersion`. Every schema
change must update shared fixtures and pass both Go and JavaScript contract
tests.

The shared fixture groups have distinct purposes:

- `valid` documents pass the schema and both runtime validators;
- `invalid` documents are well-formed JSON rejected by the schema;
- `runtime-invalid` documents pass structural schema validation but violate a
  runtime invariant such as matching browser identities;
- `malformed` documents are rejected by JSON parsing before schema validation.

Pairing is part of the authenticated `hello` exchange in v1: the first hello
contains `pairingCode`, and subsequent hellos contain `credential`. A separate
`pair` message is intentionally not part of the v1 state machine.
