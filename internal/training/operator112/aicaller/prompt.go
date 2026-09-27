package aicaller

import (
	"regexp"
	"strings"

	"emsim/internal/content"
	"emsim/internal/platform/llm"
	"emsim/internal/training"
)

// PromptVersion identifies this prompt layout — recorded in every
// model-produced turn's training.IntakeCallerGeneration.PromptVersion
// (RFC-001 §6 extended to caller replies, ADR-025) so a later change to
// staticRules or the instruction texts below is visible in old evidence
// without having to diff the deployed code against the evidence's own
// timestamp.
const PromptVersion = "caller-prompt/v1"

// staticRules and the three instructionAnswer*/instructionCalmDown/
// instructionGeneric texts are carried over verbatim from the local
// MVP's caller.py _system_prompt (slice-112-5b-plan.md's decision 4) —
// only their position changed. caller.py placed the per-turn facts/task
// block inside the system message, ahead of the conversation history;
// llama.cpp's prefix/KV cache only reuses a system+history prefix that
// stays byte-identical between calls, and a block that changes every
// turn placed before the history invalidates that cache on every single
// reply. BuildMessages instead keeps the system message limited to
// staticRules+persona (identical for the whole dialogue) and appends the
// per-turn facts/task block to the final user message, after the
// unchanged history.
const staticRules = `Ты играешь заявителя в учебном телефонном разговоре со службой 112.
Собеседник играет оператора. Продолжи разговор одной репликой заявителя.

ПРАВИЛА СОДЕРЖАНИЯ
1. Источник сведений о твоей ситуации — блок «Факты».
Сообщай только сведения из этого блока. Не добавляй обстоятельств
ради правдоподобия или эмоциональности.
2. Сначала ответь по существу последней реплики оператора.
Если он задал несколько вопросов, ответь на каждый, для которого
предоставлены сведения. Не перечисляй остальные факты.
3. Сохраняй точный смысл сведений и степень уверенности.
«Не знаю», «не могу проверить» и «этого нет» — разные ответы.
Не превращай предположение в достоверный факт.
4. Если нужные сведения обозначены как неизвестные, скажи об этом.
Если вопрос непонятен, коротко переспроси.
Если спрашивают твой возраст, не подменяй его возрастом пострадавшего.
Если нет сведений о других пострадавших, не утверждай, что их нет.
5. История нужна для связности разговора. Ошибки в предыдущих ответах
и предположения оператора не изменяют факты текущего кейса.

ПРАВИЛА РЕЧИ
6. Говори от первого лица, по-русски, разговорно и грамматически правильно.
Обычно достаточно одного-двух коротких предложений.
7. Передавай эмоциональное состояние словами и короткими паузами.
Не добавляй новые симптомы, действия или события для усиления эмоций.
8. На повторный вопрос можно повторить точные сведения.
Не повторяй без необходимости всю историю происшествия.
9. На постороннюю просьбу коротко отреагируй в роли заявителя
и вернись к причине звонка.

ФОРМАТ
Выведи только слова заявителя, готовые к озвучиванию:
без пояснений, заголовков, описания жестов и служебных обозначений.`

const instructionAnswerFacts = `Ответь на заданный вопрос по существу, опираясь на факты. Сохрани их смысл и степень уверенности. Остальные сведения не перечисляй.`

const instructionCalmDown = `Коротко отреагируй на просьбу успокоиться или говорить спокойнее. Можно выразить эмоцию персонажа и сослаться на уже сообщённые обстоятельства. Новых сведений для сообщения нет.`

const instructionGeneric = `Коротко отреагируй на последнюю реплику оператора, оставаясь в роли заявителя. Новых сведений для сообщения нет. Если он спрашивает о сведениях, которых нет в фактах, скажи, что не знаешь; если вопрос непонятен, переспроси.`

// calmingPattern is caller-prompt/v1's own fixed trigger for
// instructionCalmDown — part of the prompt layout itself, not
// scenario-authored data, so it stays a package constant rather than an
// ask_patterns-style scenario field.
var calmingPattern = regexp.MustCompile(`(?i)успокой|не крич|говорите нормально`)

// systemPrompt is staticRules plus the scenario's own persona — the
// exact text BuildMessages sends as the system message, byte-identical
// across every turn of the same dialogue (persona never changes mid-
// conversation), which is what keeps it eligible for llama.cpp's prefix
// cache.
func systemPrompt(persona string) string {
	return staticRules + "\n\nПЕРСОНАЖ\n" + persona
}

// BuildMessages lays out one caller.reply model call: system
// (staticRules+persona) followed by transcript's own history (every line
// but the one currently being answered, mapped operator->user/
// caller->assistant) followed by a final user message — the current
// operator line with the per-turn facts/task block appended
// (PromptVersion's whole point, see staticRules' doc comment). facts
// must be in their own scenario order; open/revealed are OpenFacts/
// RevealedFacts's own output for this exact transcript, and askedThisTurn
// is AskedFacts(facts, currentMessage) — the caller already needed all
// three to decide the no-model paths (opening/scripted) before calling
// this, so they are passed in rather than recomputed.
func BuildMessages(persona string, facts []content.Intake112Fact, transcript []training.IntakeLine, open, revealed map[string]bool, askedThisTurn []string, currentMessage string) []llm.Message {
	history := transcript
	if len(history) > 0 {
		history = history[:len(history)-1]
	}
	messages := systemAndHistory(persona, history)
	block := dynamicBlock(facts, open, revealed, askedThisTurn, currentMessage)
	messages = append(messages, llm.Message{Role: "user", Content: currentMessage + "\n\n" + block})
	return messages
}

// WarmupMessages is the prompt-cache warm-up request for a dialogue whose
// transcript so far is transcript (ADR-029): the system message and the
// whole history, mapped exactly as BuildMessages maps them, followed by an
// empty user message. The next real reply's BuildMessages starts with
// the very same messages — its history is this transcript, and only its
// final user message differs — so a server with a prefix cache
// (llama-server) has already processed everything but the operator's new
// line and the per-turn facts block when that reply is requested.
func WarmupMessages(persona string, transcript []training.IntakeLine) []llm.Message {
	return append(systemAndHistory(persona, transcript), llm.Message{Role: "user", Content: ""})
}

// systemAndHistory is the part of a caller prompt that stays
// byte-identical across a dialogue's turns: the system message followed
// by history, operator lines as "user" and caller lines as "assistant".
func systemAndHistory(persona string, history []training.IntakeLine) []llm.Message {
	messages := []llm.Message{{Role: "system", Content: systemPrompt(persona)}}
	for _, line := range history {
		role := "assistant"
		if line.Speaker == "operator" {
			role = "user"
		}
		messages = append(messages, llm.Message{Role: role, Content: line.Text})
	}
	return messages
}

func dynamicBlock(facts []content.Intake112Fact, open, revealed map[string]bool, askedThisTurn []string, currentMessage string) string {
	var lines []string
	for _, fact := range facts {
		if !open[fact.ID] {
			continue
		}
		lines = append(lines, factLine(fact, revealed[fact.ID]))
	}
	factsBlock := "- (нет)"
	if len(lines) > 0 {
		factsBlock = strings.Join(lines, "\n")
	}
	return "ФАКТЫ\n" + factsBlock + "\n\nЗАДАЧА ТЕКУЩЕЙ РЕПЛИКИ\n" + currentInstruction(askedThisTurn, currentMessage)
}

func factLine(fact content.Intake112Fact, revealed bool) string {
	if fact.Knowledge == "unknown" {
		return "- " + fact.Label + ": не знает"
	}
	status := "ещё не сообщил"
	if revealed {
		status = "уже сообщил"
	}
	return "- " + fact.Statement + " (" + status + ")"
}

func currentInstruction(askedThisTurn []string, currentMessage string) string {
	switch {
	case len(askedThisTurn) > 0:
		return instructionAnswerFacts
	case calmingPattern.MatchString(currentMessage):
		return instructionCalmDown
	default:
		return instructionGeneric
	}
}
