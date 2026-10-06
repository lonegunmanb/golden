# Golden

Golden is a project implemented in Go language. It is a DSL (Domain Specific Language) engine that uses HCL (HashiCorp Configuration Language) as its base, like [`grept`](https://github.com/Azure/grept).

Golden assumes a DSL engine which is composited by Plan phase and Apply phase, just like [Terraform](https://www.terraform.io/).

It supports two block interfaces: [`PlanBlock`](./plan_block.go) and [`ApplyBlock`](./apply_block.go), you can implement your own block type, in Terraform, there are `data`, `resource`, `local`, `variable`, `output`. In `grept`, there are `data`, `rule`, `fix`, `local`.

Golden has implemented `local` block.

Golden has implemented support for `for_each` and `precondition` in blocks.

A simple example to show how to customize your own DSL is in our roadmap.

## Marked values during decoding

The default `Decode` path accepts marked strings in Go `string` and `*string`
fields, including nested blocks. The Go field contains the string needed by
the credential consumer; a marked null string still decodes to a nil pointer.
Marks remain on `cty.Value` fields and are restored when decoded Go fields
are exposed through `Value` or the default block evaluation context.
`CtyValueToString` redacts values marked with `golden.SensitiveMark`
(`"sensitive"`); `BlockToString` and validation diagnostics redact blocks
with decoded Go fields carrying that mark. Unrelated marks remain available
and are not treated as secrets. Applications using a different secret mark
must handle redaction themselves or mark values with `golden.SensitiveMark`
before decoding.

Ordinary Go strings cannot carry marks. Do not print or serialize a sensitive
Go field directly, or include its value in errors, plans, or logs. Use
`cty.Value` for other marked types, or implement `CustomDecode` when
application-specific handling is needed. Marked values destined for other
ordinary Go field types still return a diagnostic instead of panicking.
