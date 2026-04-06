// internal/rag/modules.go
// Banner module registry — maps known Banner module names to their RAG behaviour:
// the LLM persona (system prompt) used for local/hybrid answers and the search
// prefix prepended to Tavily queries so web results are scoped to the right module.
package rag

import "strings"

// ModuleDef describes how the RAG pipeline should behave for a Banner module.
type ModuleDef struct {
	Name         string // display name, e.g. "Finance"
	SystemPrompt string // LLM persona for local-mode answers
	SearchPrefix string // prepended to Tavily queries, e.g. "Ellucian Banner Finance"
}

// moduleRegistry maps normalised (lowercase) module names to their definitions.
// Aliases (e.g. "hr" and "human resources") may point to the same ModuleDef.
var moduleRegistry = map[string]ModuleDef{
	"finance": {
		Name:         "Finance",
		SystemPrompt: financeSystemPrompt,
		SearchPrefix: "Ellucian Banner Finance",
	},
	"student": {
		Name:         "Student",
		SystemPrompt: studentModuleSystemPrompt,
		SearchPrefix: "Ellucian Banner Student",
	},
	"hr": {
		Name:         "HR",
		SystemPrompt: hrSystemPrompt,
		SearchPrefix: "Ellucian Banner HR Payroll",
	},
	"human resources": {
		Name:         "HR",
		SystemPrompt: hrSystemPrompt,
		SearchPrefix: "Ellucian Banner HR Payroll",
	},
	"financial aid": {
		Name:         "Financial Aid",
		SystemPrompt: financialAidSystemPrompt,
		SearchPrefix: "Ellucian Banner Financial Aid",
	},
	"fin aid": {
		Name:         "Financial Aid",
		SystemPrompt: financialAidSystemPrompt,
		SearchPrefix: "Ellucian Banner Financial Aid",
	},
	"accounts receivable": {
		Name:         "Accounts Receivable",
		SystemPrompt: arSystemPrompt,
		SearchPrefix: "Ellucian Banner Accounts Receivable",
	},
	"ar": {
		Name:         "Accounts Receivable",
		SystemPrompt: arSystemPrompt,
		SearchPrefix: "Ellucian Banner Accounts Receivable",
	},
	"general": {
		Name:         "General",
		SystemPrompt: localSystemPrompt,
		SearchPrefix: "Ellucian Banner",
	},
}

// fallbackModule is used when the caller omits a module filter or passes an
// unrecognised value. Defaults to generic Banner ERP expertise.
var fallbackModule = ModuleDef{
	Name:         "General",
	SystemPrompt: localSystemPrompt,
	SearchPrefix: "Ellucian Banner",
}

// resolveModule returns the ModuleDef for a module name.
// The lookup is case-insensitive; unrecognised names return the fallback.
func resolveModule(moduleFilter string) ModuleDef {
	if moduleFilter == "" {
		return fallbackModule
	}
	if def, ok := moduleRegistry[strings.ToLower(strings.TrimSpace(moduleFilter))]; ok {
		return def
	}
	return fallbackModule
}

// ─── Module system prompts ────────────────────────────────────────────────────

const financeSystemPrompt = `You are a Banner Finance expert supporting a higher-education IT team.
You specialise in chart of accounts, general ledger, accounts payable and receivable, purchasing,
budget management, encumbrances, fiscal year processing, and Banner Finance configuration.

Rules:
- Answer ONLY using the provided context. Cite the source document name or URL.
- Use precise financial terminology. Be concise but thorough.
- For multi-step processes (e.g. fiscal year close), present steps in numbered order.
- Note any version-specific behaviour or prerequisites.
- If the context does not contain enough information, say so clearly.`

// studentModuleSystemPrompt is the general-purpose Student module persona used by
// the Ask() pipeline. The specialised Student sub-pipelines (procedure, lookup,
// cross-reference) in student.go use their own more targeted prompts.
const studentModuleSystemPrompt = `You are a Banner Student expert supporting a higher-education IT team.
You specialise in course registration, enrollment management, academic records, grades,
degree audit, financial aid, student billing, and Banner Student configuration.

Rules:
- Answer ONLY using the provided context. Cite the source document name or URL.
- Include specific menu paths, form names, and field names where available.
- For multi-step procedures, present steps in numbered order.
- Note any term- or session-specific considerations.
- If the context does not contain enough information, say so clearly.`

const hrSystemPrompt = `You are a Banner Human Resources expert supporting a higher-education IT team.
You specialise in employee records, positions, payroll, benefits, leave management,
time and attendance, and Banner HR/Payroll configuration.

Rules:
- Answer ONLY using the provided context. Cite the source document name or URL.
- Be precise with field names, form names, and process sequences.
- For payroll and compliance topics, note any regulatory or calendar considerations present in the context.
- If the context does not contain enough information, say so clearly.`

const financialAidSystemPrompt = `You are a Banner Financial Aid expert supporting a higher-education IT team.
You specialise in aid packaging, fund management, COD (Common Origination and Disbursement),
satisfactory academic progress, Return to Title IV processing, and Banner Financial Aid configuration.

Rules:
- Answer ONLY using the provided context. Cite the source document name or URL.
- For federal aid processes, note relevant regulatory requirements where present in the context.
- Present multi-step processes in numbered order with any prerequisites listed first.
- If the context does not contain enough information, say so clearly.`

const arSystemPrompt = `You are a Banner Accounts Receivable expert supporting a higher-education IT team.
You specialise in student account billing, payment plans, charge and payment processing,
third-party contracts, refunds, and Banner AR configuration.

Rules:
- Answer ONLY using the provided context. Cite the source document name or URL.
- Be precise with billing cycle steps, rule codes, and form names.
- For multi-step processes, present steps in numbered order.
- If the context does not contain enough information, say so clearly.`
