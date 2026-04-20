package model

// SchemaJSON is the JSON Schema the model is constrained to.
// Local backends pass this as a GBNF grammar derived by the server;
// cloud backends pass it as response_format.json_schema.
const SchemaJSON = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["intent_summary", "approach"],
  "properties": {
    "intent_summary": { "type": "string", "minLength": 1 },
    "approach": {
      "type": "string",
      "enum": ["command", "script", "tool_call", "clarify", "refuse", "inform"]
    },
    "command": { "type": "string" },
    "script": {
      "type": "object",
      "additionalProperties": false,
      "required": ["interpreter", "body"],
      "properties": {
        "interpreter": { "type": "string" },
        "body":        { "type": "string" }
      }
    },
    "tool_call": {
      "type": "object",
      "additionalProperties": false,
      "required": ["name", "arguments"],
      "properties": {
        "name": {
          "type": "string",
          "enum": ["list_dir","read_file","head_file","which","stat","env_get","cwd","os_info","git_status"]
        },
        "arguments": { "type": "object" }
      }
    },
    "clarifying_question": { "type": "string" },
    "refusal_reason": { "type": "string" },
    "stdout_to_user": { "type": "string" },
    "description": { "type": "string" },
    "risk": {
      "type": "string",
      "enum": ["safe","network","mutates","destructive","sudo"]
    },
    "needs_sudo": { "type": "boolean" },
    "expected_runtime": {
      "type": "string",
      "enum": ["instant","seconds","minutes","long"]
    },
    "alternatives": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["command", "description", "risk"],
        "properties": {
          "command":     { "type": "string" },
          "description": { "type": "string" },
          "risk":        { "type": "string" }
        }
      }
    },
    "confidence": {
      "type": "string",
      "enum": ["low","medium","high"]
    }
  }
}`
