package model

// SchemaJSON is the JSON Schema the model is constrained to.
//
// We use `oneOf` so that each `approach` value enforces the fields it
// actually needs at sampling time. llama.cpp's grammar generator turns
// this into a GBNF grammar that physically prevents the model from
// emitting an `approach: "command"` object that lacks the `command`
// string. This matters at the small-model end of the spectrum, where
// Qwen2.5-Coder-1.5B will happily emit a half-formed proposal otherwise.
//
// We deliberately keep the per-branch property sets identical except for
// the discriminator and required fields. Cloud backends that don't
// support `oneOf` in their JSON-schema response_format flatten this in
// the backend's fixup pass.
const SchemaJSON = `{
  "type": "object",
  "oneOf": [
    {
      "type": "object",
      "additionalProperties": false,
      "required": ["approach", "command", "description", "risk"],
      "properties": {
        "approach":         { "const": "command" },
        "command":          { "type": "string", "minLength": 1 },
        "description":      { "type": "string", "minLength": 1 },
        "risk":             { "enum": ["safe","network","mutates","destructive","sudo"] },
        "needs_sudo":       { "type": "boolean" },
        "expected_runtime": { "enum": ["instant","seconds","minutes","long"] },
        "confidence":       { "enum": ["low","medium","high"] }
      }
    },
    {
      "type": "object",
      "additionalProperties": false,
      "required": ["approach", "script", "description", "risk"],
      "properties": {
        "approach":         { "const": "script" },
        "script": {
          "type": "object",
          "additionalProperties": false,
          "required": ["interpreter", "body"],
          "properties": {
            "interpreter": { "type": "string", "minLength": 1 },
            "body":        { "type": "string", "minLength": 1 }
          }
        },
        "description":      { "type": "string", "minLength": 1 },
        "risk":             { "enum": ["safe","network","mutates","destructive","sudo"] },
        "needs_sudo":       { "type": "boolean" },
        "expected_runtime": { "enum": ["instant","seconds","minutes","long"] },
        "confidence":       { "enum": ["low","medium","high"] }
      }
    },
    {
      "type": "object",
      "additionalProperties": false,
      "required": ["approach", "tool_call", "description"],
      "properties": {
        "approach":    { "const": "tool_call" },
        "tool_call": {
          "type": "object",
          "additionalProperties": false,
          "required": ["name", "arguments"],
          "properties": {
            "name": {
              "enum": ["list_dir","read_file","head_file","which","stat","env_get","cwd","os_info","git_status","help","grep","find_files","web_fetch","ask_user"]
            },
            "arguments": { "type": "object" }
          }
        },
        "description": { "type": "string", "minLength": 1 },
        "risk":        { "enum": ["safe","network","mutates","destructive","sudo"] }
      }
    },
    {
      "type": "object",
      "additionalProperties": false,
      "required": ["approach", "stdout_to_user"],
      "properties": {
        "approach":       { "const": "inform" },
        "stdout_to_user": { "type": "string", "minLength": 1 },
        "description":    { "type": "string" },
        "risk":           { "enum": ["safe","network","mutates","destructive","sudo"] }
      }
    },
    {
      "type": "object",
      "additionalProperties": false,
      "required": ["approach", "clarifying_question"],
      "properties": {
        "approach":            { "const": "clarify" },
        "clarifying_question": { "type": "string", "minLength": 1 },
        "description":         { "type": "string" },
        "risk":                { "enum": ["safe","network","mutates","destructive","sudo"] }
      }
    },
    {
      "type": "object",
      "additionalProperties": false,
      "required": ["approach", "refusal_reason"],
      "properties": {
        "approach":       { "const": "refuse" },
        "refusal_reason": { "type": "string", "minLength": 1 },
        "description":    { "type": "string" },
        "risk":           { "enum": ["safe","network","mutates","destructive","sudo"] }
      }
    }
  ]
}`
