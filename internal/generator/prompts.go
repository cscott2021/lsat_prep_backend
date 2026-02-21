package generator

import (
	"fmt"
	"strings"

	"github.com/lsat-prep/backend/internal/models"
)

var subtypeStems = map[models.LRSubtype][]string{
	models.SubtypeStrengthen: {
		"Which of the following, if true, most strengthens the argument?",
		"Which of the following, if true, most strongly supports the argument above?",
	},
	models.SubtypeWeaken: {
		"Which of the following, if true, most weakens the argument?",
		"Which of the following, if true, most seriously undermines the argument above?",
	},
	models.SubtypeAssumption: {
		"The argument relies on which of the following assumptions?",
		"Which of the following is an assumption on which the argument depends?",
		"The argument assumes which of the following?",
	},
	models.SubtypeFlaw: {
		"The reasoning in the argument is flawed because it",
		"The reasoning in the argument is most vulnerable to criticism because it",
		"Which of the following most accurately describes a flaw in the argument?",
	},
	models.SubtypeMustBeTrue: {
		"If the statements above are true, which of the following must also be true?",
		"Which of the following can be properly inferred from the statements above?",
	},
	models.SubtypeMostStrongly: {
		"Which of the following is most strongly supported by the information above?",
		"The statements above, if true, most strongly support which of the following?",
	},
	models.SubtypeMainConclusion: {
		"Which of the following most accurately expresses the main conclusion of the argument?",
		"The main point of the argument is that",
	},
	models.SubtypeMethodReasoning: {
		"The argument proceeds by",
		"Which of the following most accurately describes the method of reasoning used in the argument?",
	},
	models.SubtypeEvaluate: {
		"Which of the following would be most useful to know in order to evaluate the argument?",
		"The answer to which of the following questions would most help in evaluating the argument?",
	},
	models.SubtypePrinciple: {
		"Which of the following principles, if valid, most helps to justify the reasoning above?",
	},
	models.SubtypeApplyPrinciple: {
		"The principle stated above, if valid, most helps to justify which of the following judgments?",
		"Which of the following judgments best illustrates the principle stated above?",
	},
	models.SubtypeParallelReasoning: {
		"Which of the following arguments is most similar in its pattern of reasoning to the argument above?",
		"The pattern of reasoning in which of the following is most similar to that in the argument above?",
	},
	models.SubtypeParallelFlaw: {
		"The flawed pattern of reasoning in which of the following is most similar to that in the argument above?",
		"Which of the following exhibits a pattern of flawed reasoning most similar to that exhibited by the argument above?",
	},
	models.SubtypeRoleOfStatement: {
		"The claim that [quoted claim] plays which of the following roles in the argument?",
		"The statement that [quoted claim] figures in the argument in which of the following ways?",
	},
}

var subtypeCorrectAnswerRules = map[models.LRSubtype]string{
	models.SubtypeStrengthen: `
CORRECT ANSWER RULES (Strengthen):
- The correct answer must provide NEW information not stated in the stimulus
- It must make the conclusion MORE likely to be true
- It typically does one of: (a) rules out an alternative explanation, (b) provides an additional premise that supports the causal link, (c) shows the mechanism by which the conclusion follows
- It should NOT merely restate a premise or the conclusion
- It should NOT be so strong that it independently proves the conclusion`,

	models.SubtypeWeaken: `
CORRECT ANSWER RULES (Weaken):
- The correct answer must provide NEW information not stated in the stimulus
- It must make the conclusion LESS likely to be true
- It typically does one of: (a) introduces a plausible alternative explanation, (b) breaks the causal link, (c) shows a flaw in the evidence, (d) provides a counterexample
- It should NOT merely contradict the conclusion — it must attack the REASONING
- It should NOT be irrelevant to the argument's logical structure`,

	models.SubtypeAssumption: `
CORRECT ANSWER RULES (Assumption — Necessary):
- The correct answer states something that MUST be true for the argument to work
- Apply the Negation Test: if you negate the correct answer, the argument should fall apart
- It fills a gap between the premises and conclusion
- It should NOT be a mere restatement of a premise
- It should NOT be something that strengthens but is not required
- It should NOT be the conclusion itself`,

	models.SubtypeFlaw: `
CORRECT ANSWER RULES (Flaw):
- The correct answer must accurately DESCRIBE the logical error in the argument
- It must be phrased in abstract/general terms describing the reasoning error
- Common flaws: confusing necessary/sufficient conditions, correlation vs causation, ad hominem, hasty generalization, equivocation, part-whole fallacy, appeal to authority, false dichotomy
- The description must match what actually happens in the stimulus — not just name a flaw that sounds plausible
- It should NOT describe the argument's conclusion as wrong — it should describe HOW the reasoning fails`,

	models.SubtypeMustBeTrue: `
CORRECT ANSWER RULES (Must Be True / Inference):
- The correct answer must be LOGICALLY ENTAILED by the stimulus — not merely likely
- If the stimulus premises are true, the correct answer cannot be false
- It is typically a logical consequence of combining two or more premises
- It should NOT require any assumptions beyond what is stated
- It should NOT go beyond the scope of the stimulus
- It should NOT be merely consistent with the stimulus — it must be required by it`,

	models.SubtypeMostStrongly: `
CORRECT ANSWER RULES (Most Strongly Supported):
- The correct answer is the claim most supported by the stimulus evidence
- Unlike Must Be True, it need not be logically entailed — just most probable given the evidence
- It should follow naturally from the information provided
- It should NOT require significant additional assumptions`,

	models.SubtypeMethodReasoning: `
CORRECT ANSWER RULES (Method of Reasoning):
- The correct answer abstractly describes the argumentative technique used
- It must accurately map onto the stimulus structure (premises → conclusion)
- Common methods: analogy, counterexample, reductio ad absurdum, appeal to evidence, elimination of alternatives, establishing a general principle
- The description must match the actual logical moves in the stimulus`,

	models.SubtypeParallelReasoning: `
CORRECT ANSWER RULES (Parallel Reasoning):
- The correct answer must replicate the EXACT logical structure of the stimulus
- Match: (a) the type of premises (conditional, causal, statistical), (b) the validity/invalidity of the reasoning, (c) the conclusion type
- Topic should differ but structure should be identical
- If the stimulus has a flaw, the correct answer must have the same flaw`,

	models.SubtypeParallelFlaw: `
CORRECT ANSWER RULES (Parallel Flaw):
- The correct answer contains an argument with the SAME logical flaw as the stimulus
- Both the structure AND the error type must match
- The topic must be completely different from the stimulus
- If the stimulus confuses necessary/sufficient, the correct answer must too
- If the stimulus makes a causal error, the correct answer must make the same kind`,

	models.SubtypePrinciple: `
CORRECT ANSWER RULES (Principle):
- The correct answer states a general rule that, if true, justifies the specific argument in the stimulus
- It must be broad enough to be a principle (not just a restatement) but specific enough to actually support this argument
- The argument's conclusion should follow from the principle + the premises`,

	models.SubtypeApplyPrinciple: `
CORRECT ANSWER RULES (Apply Principle):
- The stimulus states a general principle or rule
- The correct answer presents a specific situation where the principle applies correctly
- The application must follow logically from the principle's conditions
- The correct answer should not require any additional principles or unstated assumptions
- All conditions of the principle must be met in the specific case`,

	models.SubtypeEvaluate: `
CORRECT ANSWER RULES (Evaluate):
- The correct answer identifies information that would help determine if the argument is sound
- Both possible answers to the question (yes/no) should have different implications for the argument
- One answer should strengthen, the other should weaken — that's what makes it useful for evaluation`,

	models.SubtypeMainConclusion: `
CORRECT ANSWER RULES (Main Conclusion):
- The correct answer is a near-paraphrase of the argument's main point
- It is what the other statements in the stimulus are trying to prove
- It should NOT be a premise, intermediate conclusion, or background information`,

	models.SubtypeRoleOfStatement: `
CORRECT ANSWER RULES (Role of a Statement):
- The correct answer describes the function a specific claim plays in the argument
- Functions: main conclusion, intermediate conclusion, premise, evidence, counterexample, background, concession
- The description must accurately characterize the relationship between the statement and the rest of the argument`,
}

var subtypeWrongAnswerRules = map[models.LRSubtype]string{
	models.SubtypeStrengthen: `
WRONG ANSWER CONSTRUCTION (Strengthen):
Each wrong answer must fall into one of these categories — label each in your explanation:
1. IRRELEVANT: True-sounding but does not connect to the argument's logical gap
2. WEAKENER: Actually undermines the argument (common trap for test-takers who confuse the task)
3. OUT OF SCOPE: Addresses a topic related to but distinct from the argument's core claim
4. RESTATES PREMISE: Merely repeats information already in the stimulus without adding support
At least one wrong answer should be a WEAKENER (the most common student error on strengthen questions).`,

	models.SubtypeWeaken: `
WRONG ANSWER CONSTRUCTION (Weaken):
1. IRRELEVANT: Sounds related but doesn't attack the reasoning
2. STRENGTHENER: Actually supports the argument (reversal trap)
3. OUT OF SCOPE: About a related but different issue
4. TOO EXTREME: Addresses an extreme version of the claim not actually made
At least one wrong answer should be a STRENGTHENER.`,

	models.SubtypeAssumption: `
WRONG ANSWER CONSTRUCTION (Assumption):
1. HELPS BUT NOT REQUIRED: Strengthens the argument but isn't necessary (fails negation test)
2. RESTATES PREMISE: Already stated in the stimulus
3. RESTATES CONCLUSION: Says what the argument concludes, not what it assumes
4. OUT OF SCOPE: Irrelevant to the argument's logical structure
The "HELPS BUT NOT REQUIRED" distractor is the hardest — it must be clearly not required when negated.`,

	models.SubtypeFlaw: `
WRONG ANSWER CONSTRUCTION (Flaw):
1. WRONG FLAW: Accurately describes a real logical flaw, but NOT the one in this argument
2. DESCRIBES THE ARGUMENT CORRECTLY: States what the argument does without identifying an error
3. TOO BROAD: Describes a flaw in vague terms that could apply to almost any argument
4. MISCHARACTERIZES: Describes something the argument doesn't actually do
The "WRONG FLAW" distractor is the classic trap — it's a valid flaw type but doesn't match this stimulus.`,

	models.SubtypeMustBeTrue: `
WRONG ANSWER CONSTRUCTION (Must Be True):
1. COULD BE TRUE: Consistent with the stimulus but not required by it
2. GOES BEYOND: Requires assumptions not in the stimulus
3. PARTIAL INFERENCE: True of some cases mentioned but overgeneralizes
4. REVERSAL: Gets a conditional relationship backwards
The key distinction: must be true vs. could be true.`,

	models.SubtypeMostStrongly: `
WRONG ANSWER CONSTRUCTION (Most Strongly Supported):
1. COULD BE TRUE: Consistent with the stimulus but not particularly supported by it
2. GOES BEYOND: Requires significant assumptions not in the stimulus
3. REVERSAL: Gets the direction of a relationship backwards
4. EXTREME LANGUAGE: Uses "always," "never," "all" when the stimulus hedges`,

	models.SubtypeMethodReasoning: `
WRONG ANSWER CONSTRUCTION (Method of Reasoning):
1. WRONG METHOD: Accurately describes a reasoning method, but not the one used here
2. PARTIAL DESCRIPTION: Captures one aspect of the argument but misses the main technique
3. MISCHARACTERIZES: Describes the argument doing something it doesn't do
4. TOO SPECIFIC/GENERAL: Either over- or under-describes the method`,

	models.SubtypeParallelReasoning: `
WRONG ANSWER CONSTRUCTION (Parallel Reasoning):
1. SAME TOPIC, WRONG STRUCTURE: Similar subject matter but different logical form
2. FLAWED WHEN ORIGINAL IS VALID: Introduces a logical error not present in the original
3. VALID WHEN ORIGINAL IS FLAWED: Fixes the error, making the reasoning valid
4. PARTIALLY PARALLEL: Matches some but not all structural elements`,

	models.SubtypeParallelFlaw: `
WRONG ANSWER CONSTRUCTION (Parallel Flaw):
1. DIFFERENT FLAW: Contains a logical error, but a different type than the stimulus
2. NO FLAW: The reasoning is actually valid (no parallel error)
3. SAME TOPIC: Mimics the stimulus subject but with a different logical structure
4. PARTIALLY PARALLEL: Matches the structure but the flaw manifests differently`,

	models.SubtypePrinciple: `
WRONG ANSWER CONSTRUCTION (Principle):
1. TOO NARROW: A principle that only covers part of the argument
2. TOO BROAD: A principle that's true but doesn't specifically justify THIS argument
3. WRONG DIRECTION: A principle that would justify the opposite conclusion
4. IRRELEVANT PRINCIPLE: A valid principle that doesn't connect to the argument's reasoning`,

	models.SubtypeApplyPrinciple: `
WRONG ANSWER CONSTRUCTION (Apply Principle):
1. VIOLATES A CONDITION: The scenario doesn't meet all conditions of the principle
2. WRONG OUTCOME: Meets the conditions but draws the wrong conclusion
3. SUPERFICIALLY SIMILAR: Shares surface features with the principle but doesn't actually apply
4. REVERSES APPLICATION: Applies the principle backwards`,

	models.SubtypeEvaluate: `
WRONG ANSWER CONSTRUCTION (Evaluate):
1. ONE-DIRECTIONAL: The answer to the question only strengthens OR weakens, not both depending on the answer
2. IRRELEVANT QUESTION: The answer wouldn't affect the argument either way
3. ALREADY ANSWERED: The stimulus already provides the information asked about
4. WRONG SCOPE: Asks about something adjacent but not central to the argument's logic`,

	models.SubtypeMainConclusion: `
WRONG ANSWER CONSTRUCTION (Main Conclusion):
1. PREMISE MASQUERADING: A premise stated in the stimulus, not the conclusion
2. INTERMEDIATE CONCLUSION: A sub-conclusion that supports the main conclusion
3. BACKGROUND INFO: Context from the stimulus that isn't argued for or against
4. OVERSTATED CONCLUSION: Exaggerates what the argument actually claims`,

	models.SubtypeRoleOfStatement: `
WRONG ANSWER CONSTRUCTION (Role of a Statement):
1. WRONG ROLE: Correctly identifies that the statement is present but mischaracterizes its function
2. CONFUSES PREMISE/CONCLUSION: Calls a premise a conclusion or vice versa
3. INVENTS A ROLE: Describes a function (like "counterexample") that the statement doesn't serve
4. RIGHT ROLE, WRONG RELATIONSHIP: Correctly names the role type but misstates what it supports or opposes`,
}

func LRSystemPrompt() string {
	return `You are an expert LSAT question writer with 20 years of experience at the Law School Admission Council (LSAC). You write questions that are indistinguishable from real LSAT Logical Reasoning questions.

Your questions must follow these exact structural rules:

STIMULUS:
- 4-7 sentences presenting an argument, scenario, or set of facts
- Contains a clear logical structure: premises leading to a conclusion
- Uses formal but accessible language — the register of a newspaper editorial, academic summary, or public policy statement
- Covers diverse topics: science, law, history, business, ethics, environment, arts, social policy, technology
- Never references the LSAT itself or test-taking
- Each stimulus must present a self-contained argument — no external knowledge needed

STIMULUS CONSTRUCTION RULES:

Reasoning patterns to use (vary across questions):
- Conditional logic: "If X then Y" chains, with conclusions drawn from contrapositives or affirming the consequent
- Causal reasoning: "X caused Y" claims with evidence that may have confounds
- Analogy: "X is like Y, so what's true of X is true of Y"
- Statistical/survey: "A study found..." or "In a survey of..."
- Appeal to evidence: Expert testimony, historical precedent, experimental results
- Principle application: General rule applied to a specific case

Language register:
- Use the voice of newspaper editorials, academic summaries, policy memos, or scientific abstracts
- No first person ("I believe")
- No slang, no contractions
- Varied sentence structures: some complex with subordinate clauses, some short and declarative
- Signal words that mark argument structure: "therefore," "however," "since," "although," "consequently," "nevertheless"

Topic diversity requirements:
- Law: constitutional issues, legal theory, court decisions, legislation
- Natural science: biology, ecology, geology, physics, chemistry, medicine
- Social science: economics, psychology, sociology, political science, anthropology
- Humanities: art, literature, philosophy, history, music, architecture
- Business/policy: regulation, urban planning, education, public health, technology
- NO questions about the LSAT itself, test prep, or academic admissions

QUESTION STEM:
- One sentence that clearly asks the student what to do
- Uses standard LSAT phrasing for the question type (provided in user prompt)

ANSWER CHOICES:
- Exactly 5 choices labeled A through E
- Each choice is 1-2 sentences
- Exactly ONE correct answer
- The 4 wrong answers must each be wrong for a specific, identifiable reason
- Wrong answers should be plausible — they must be genuinely tempting, not obviously dismissable
- At least one wrong answer should be a "close second" that tests the most common mistake for this question type
- Choices should vary in structure and length — not all the same sentence pattern

EXPLANATIONS:
- For the correct answer: 2-4 sentences explaining precisely WHY it is correct, referencing the logical structure of the stimulus
- For each wrong answer: 1-2 sentences explaining precisely WHY it is wrong — name the specific logical error (e.g., "irrelevant comparison," "out of scope," "reverses the relationship") and label its wrong answer archetype

DIFFICULTY CALIBRATION:
- Easy: Straightforward argument with a clear flaw/assumption. The correct answer is noticeably stronger than alternatives. One strong distractor.
- Medium: More nuanced argument, possibly with multiple premises. Two strong distractors. Requires careful reading.
- Hard: Complex argument with subtle reasoning. The correct and "close second" answers require distinguishing between very similar logical moves. Three strong distractors.

You must respond with valid JSON only. No markdown, no explanation outside the JSON.`
}

func RCSystemPrompt() string {
	return `You are an expert LSAT question writer with 20 years of experience at the Law School Admission Council (LSAC). You write Reading Comprehension passages and questions that are indistinguishable from real LSAT material.

RC PASSAGE CONSTRUCTION:

Structure (450-500 words, 3-4 paragraphs):
- Paragraph 1: Introduce the topic and the main thesis or debate
- Paragraph 2: Develop the main argument with evidence, examples, or analysis
- Paragraph 3: Present a counterargument, complication, or alternative perspective
- Paragraph 4 (optional): Resolve, synthesize, or state implications

Required elements:
- A clear MAIN POINT that can be identified (for main idea questions)
- At least 3 SPECIFIC DETAILS that questions can reference (dates, names, percentages, specific claims)
- At least 1 AUTHOR'S ATTITUDE signal (words revealing agreement, skepticism, enthusiasm, caution)
- At least 2 STRUCTURAL TRANSITIONS between ideas ("however," "in contrast," "furthermore")
- Language that supports INFERENCE but doesn't state everything explicitly

Subject areas (rotate across batches):
1. Law: legal theory, landmark cases, constitutional interpretation, comparative law
2. Natural science: evolutionary biology, ecology, climate, geology, physics
3. Social science: economic theory, psychological research, sociological analysis
4. Humanities: literary criticism, art history, philosophical debates, music theory

Comparative passages (when requested):
- Passage A: ~225 words presenting one perspective
- Passage B: ~225 words presenting a different perspective on the same topic
- Both passages must be self-contained but share enough overlap for comparison questions

RC QUESTION TYPES (generate 5-8 per passage):
1. Main Point / Primary Purpose (1 per passage)
   - Correct: captures the central thesis
   - Wrong: too narrow (one paragraph only), too broad, misidentifies the purpose

2. Specific Detail (1-2 per passage)
   - Correct: accurately restates a specific claim from the passage
   - Wrong: distorts the detail, confuses which paragraph, attributes to wrong source

3. Inference (1-2 per passage)
   - Correct: logically follows from passage content without being explicitly stated
   - Wrong: requires outside knowledge, overgeneralizes, reverses a relationship

4. Author's Attitude/Tone (0-1 per passage)
   - Correct: matches the attitude signals in the text
   - Wrong: too extreme, opposite tone, confuses attitude toward topic vs. attitude toward a cited source

5. Function of a Phrase/Paragraph (1 per passage)
   - Correct: accurately describes the rhetorical role
   - Wrong: identifies the content but mischaracterizes its purpose

6. Strengthen/Weaken passage-based (0-1 per passage)
   - Same rules as LR strengthen/weaken but grounded in passage claims

7. Comparative Passage Questions (for comparative passages only)
   - "Both authors would agree..."
   - "Unlike Passage A, Passage B..."
   - Correct: accurately reflects the relationship between the two perspectives

RC WRONG ANSWER PRINCIPLES:
- The "conservative and boring" principle: correct RC answers tend to be carefully hedged and understated
- Wrong answers are often MORE interesting or specific than the correct answer
- Common wrong answer types:
  * DISTORTION: Takes a passage idea and subtly changes it
  * TOO BROAD: Overgeneralizes beyond what the passage supports
  * TOO NARROW: Captures only one detail, missing the bigger picture
  * OUT OF SCOPE: Introduces ideas not discussed in the passage
  * REVERSED RELATIONSHIP: Gets the causal or comparative direction wrong
  * WRONG PARAGRAPH: Attributes information to the wrong part of the passage

ANSWER CHOICES:
- Exactly 5 choices labeled A through E per question
- Each choice is 1-2 sentences
- Exactly ONE correct answer per question

EXPLANATIONS:
- For the correct answer: 2-4 sentences referencing specific passage content
- For each wrong answer: 1-2 sentences naming the specific error type from the list above

DIFFICULTY CALIBRATION:
- Easy: Main idea and detail questions with clear passage support. One strong distractor.
- Medium: Inference and function questions requiring synthesis. Two strong distractors.
- Hard: Subtle inference, author tone, and comparative questions. Three strong distractors.

You must respond with valid JSON only. No markdown, no explanation outside the JSON.`
}

func BuildLRUserPrompt(subtype models.LRSubtype, difficulty models.Difficulty, count int) string {
	stems := subtypeStems[subtype]
	var stemLines string
	for _, s := range stems {
		stemLines += fmt.Sprintf("- %s\n", s)
	}

	correctRules := subtypeCorrectAnswerRules[subtype]
	wrongRules := subtypeWrongAnswerRules[subtype]

	return fmt.Sprintf(`Generate exactly %d LSAT Logical Reasoning questions.

Section: Logical Reasoning
Question type: %s
Difficulty: %s

Standard question stems for this type:
%s
%s

%s

Respond with this exact JSON structure:
{
  "questions": [
    {
      "stimulus": "...",
      "question_stem": "...",
      "choices": [
        {"id": "A", "text": "...", "explanation": "...", "wrong_answer_type": "irrelevant"},
        {"id": "B", "text": "...", "explanation": "...", "wrong_answer_type": null},
        {"id": "C", "text": "...", "explanation": "...", "wrong_answer_type": "out_of_scope"},
        {"id": "D", "text": "...", "explanation": "...", "wrong_answer_type": "weakener"},
        {"id": "E", "text": "...", "explanation": "...", "wrong_answer_type": "restates_premise"}
      ],
      "correct_answer_id": "B",
      "explanation": "..."
    }
  ]
}

Requirements:
- Each question must cover a DIFFERENT topic — no two questions in the same batch about the same subject
- Vary the position of the correct answer across A-E — do not cluster correct answers
- The correct answer position distribution across the batch should be roughly uniform
- For the correct answer choice, set "wrong_answer_type" to null
- For each wrong answer choice, set "wrong_answer_type" to one of the archetype labels specified in the wrong answer rules above`,
		count, string(subtype), string(difficulty), stemLines, correctRules, wrongRules)
}

func BuildRCUserPrompt(difficulty models.Difficulty, questionsPerPassage int) string {
	return fmt.Sprintf(`Generate a Reading Comprehension passage with %d questions.

Difficulty: %s

Respond with this exact JSON structure:
{
  "passage": {
    "title": "...",
    "subject_area": "law",
    "content": "... (450-500 words) ...",
    "is_comparative": false,
    "passage_b": null
  },
  "questions": [
    {
      "stimulus": "",
      "question_stem": "The main purpose of the passage is to...",
      "choices": [
        {"id": "A", "text": "...", "explanation": "...", "wrong_answer_type": "too_broad"},
        {"id": "B", "text": "...", "explanation": "...", "wrong_answer_type": null},
        {"id": "C", "text": "...", "explanation": "...", "wrong_answer_type": "too_narrow"},
        {"id": "D", "text": "...", "explanation": "...", "wrong_answer_type": "distortion"},
        {"id": "E", "text": "...", "explanation": "...", "wrong_answer_type": "out_of_scope"}
      ],
      "correct_answer_id": "B",
      "explanation": "..."
    }
  ]
}

Requirements:
- For RC questions, the "stimulus" field should be empty (the passage IS the stimulus)
- Vary question types across the set as specified in the system prompt
- Vary the position of correct answers across A-E
- Include at least one Main Point question and at least one Inference question
- For the correct answer choice, set "wrong_answer_type" to null
- For each wrong answer choice, set "wrong_answer_type" to one of: distortion, too_broad, too_narrow, out_of_scope, reversed_relationship, wrong_paragraph`,
		questionsPerPassage, string(difficulty))
}

// GetSubtypeStems returns the question stems for a given subtype.
func GetSubtypeStems(subtype models.LRSubtype) []string {
	return subtypeStems[subtype]
}

// GetCorrectAnswerRules returns the correct answer rules for a given subtype.
func GetCorrectAnswerRules(subtype models.LRSubtype) string {
	return subtypeCorrectAnswerRules[subtype]
}

// GetWrongAnswerRules returns the wrong answer rules for a given subtype.
func GetWrongAnswerRules(subtype models.LRSubtype) string {
	return subtypeWrongAnswerRules[subtype]
}

// SubtypeDisplayName returns a human-readable name for a subtype.
func SubtypeDisplayName(subtype models.LRSubtype) string {
	return strings.ReplaceAll(string(subtype), "_", " ")
}
