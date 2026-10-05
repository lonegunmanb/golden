# Golden

Golden is a project implemented in Go language. It is a DSL (Domain Specific Language) engine that uses HCL (HashiCorp Configuration Language) as its base, like [`grept`](https://github.com/Azure/grept).

Golden assumes a DSL engine which is composited by Plan phase and Apply phase, just like [Terraform](https://www.terraform.io/).

It supports two block interfaces: [`PlanBlock`](./plan_block.go) and [`ApplyBlock`](./apply_block.go), you can implement your own block type, in Terraform, there are `data`, `resource`, `local`, `variable`, `output`. In `grept`, there are `data`, `rule`, `fix`, `local`.

Golden has implemented `local` block.

Golden has implemented support for `for_each` and `precondition` in blocks.

A simple example to show how to customize your own DSL is in our roadmap.

## Marked values during decoding

The default `Decode` path preserves cty marks when an HCL attribute is decoded
into a `cty.Value` field. Ordinary Go fields (including nested `string` and
`*string` fields) cannot carry marks: decoding a marked non-null value into one
returns a diagnostic instead of discarding its marks or panicking. A marked
null string can still decode into a nil `*string`.

For sensitive credentials, use a `cty.Value` field or implement `CustomDecode`
to handle marked values explicitly. If a custom decoder unwraps a secret to
pass it to a credential consumer, it must preserve redaction in diagnostics,
plans, logs, and outputs.
