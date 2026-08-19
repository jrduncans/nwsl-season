package before_resolution

import rego.v1

# Free-form string dimensions need concrete values so readers and tooling can
# distinguish identifiers from human-readable labels. Enums are excluded
# because their member lists already provide the complete bounded examples.
deny contains attribute_finding(
	"nwsl_string_examples",
	group.id,
	attr.id,
	"NWSL string attributes must define at least one example",
) if {
	group := input.groups[_]
	attr := group.attributes[_]
	startswith(attr.id, "nwsl.")
	is_open_string(attr)
	count(object.get(attr, "examples", [])) == 0
}

# Cardinality guidance may live on an individual dimension or on its enclosing
# group when the group classifies a family of related fields together.
deny contains attribute_finding(
	"nwsl_string_cardinality",
	group.id,
	attr.id,
	"NWSL string attributes must document whether their values are bounded, low-cardinality, or high-cardinality",
) if {
	group := input.groups[_]
	attr := group.attributes[_]
	startswith(attr.id, "nwsl.")
	is_open_string(attr)
	not has_cardinality_guidance(group, attr)
}

is_open_string(attr) if {
	attr.type == "string"
}

is_open_string(attr) if {
	attr.type == "string[]"
}

has_cardinality_guidance(group, attr) if {
	text := lower(sprintf(
		"%s %s %s %s",
		[
			object.get(group, "brief", ""),
			object.get(group, "note", ""),
			object.get(attr, "brief", ""),
			object.get(attr, "note", ""),
		],
	))
	some marker in {"bounded", "high-cardinality", "low-cardinality", "unbounded"}
	contains(text, marker)
}

attribute_finding(finding_id, group_id, attr_id, message) := {
	"id": finding_id,
	"context": {
		"id": "nwsl_attribute_governance",
		"group": group_id,
		"attr": attr_id,
	},
	"message": sprintf("%s: %s", [message, attr_id]),
	"level": "violation",
}
