package prompts

import _ "embed"

//go:embed system.txt
var SystemPrompt string

//go:embed user.txt
var UserPromptTemplate string
