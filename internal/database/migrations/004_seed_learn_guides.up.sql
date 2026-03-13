INSERT INTO learn_guides (section, subtype, overview, common_stems, steps, tips, examples, display_order)
VALUES

-- 1. Strengthen
('logical_reasoning', 'strengthen',
 'Strengthen questions ask you to find a piece of evidence that, if true, would make the argument''s conclusion more likely to be correct. The correct answer supports the argument by providing new information that closes a gap in the reasoning.',
 ARRAY[
   'Which one of the following, if true, most strengthens the argument?',
   'Which one of the following, if true, most supports the conclusion drawn above?',
   'Which one of the following, if assumed, would provide the most support for the argument?'
 ],
 '[{"title":"Find the conclusion","body":"Identify the main claim the argument is trying to establish before anything else."},{"title":"Map the evidence","body":"Note what premises the argument provides in support of the conclusion."},{"title":"Spot the gap","body":"Ask yourself what assumption bridges the evidence to the conclusion — this is where strengthening evidence will plug in."},{"title":"Predict the type of answer","body":"Before reading the choices, decide what kind of information would make the conclusion more likely to be true."},{"title":"Evaluate each choice","body":"For each option, ask: if this were true, does the conclusion become more likely? Eliminate choices that are irrelevant, out of scope, or actually weaken."},{"title":"Confirm your answer","body":"Re-read the argument with your chosen answer added and verify it genuinely makes the conclusion more defensible."}]',
 ARRAY[
   'The correct answer doesn''t have to prove the conclusion — it just needs to make it more likely.',
   'For causal arguments, look for answers that rule out alternative explanations.',
   'Scope matters — the correct answer must address the specific gap in this argument.',
   'Strengthen EXCEPT questions flip the task: four choices strengthen; you pick the one that does not.'
 ],
 '[{"stimulus":"A city required all new commercial buildings to install green roofs — rooftop gardens that absorb rainwater. City engineers argued that this policy would reduce the frequency of basement flooding in adjacent residential buildings. A study of twelve cities found that those with extensive green roof coverage experienced 30 percent fewer flooding incidents than comparable cities without green roofs.","question_stem":"Which one of the following, if true, most strengthens the argument above?","choices":[{"label":"A","text":"The city already has among the lowest flooding rates in the region."},{"label":"B","text":"Green roofs require more maintenance than traditional roofs."},{"label":"C","text":"The twelve cities in the study have climates and soil drainage profiles similar to this city."},{"label":"D","text":"Some homeowners in the city have voluntarily installed green roofs on their own buildings."},{"label":"E","text":"The green roof mandate applies only to buildings over five stories."}],"correct_index":2,"explanation":"Choice (C) is correct. The argument relies on a study of other cities to support its conclusion. If those cities are not comparable to this one, the study''s findings may not apply here. Choice (C) establishes that the study cities are meaningfully similar, making the evidence more directly applicable and therefore strengthening the argument."}]',
 1),

-- 2. Weaken
('logical_reasoning', 'weaken',
 'Weaken questions ask you to identify a piece of information that, if true, would make the argument''s conclusion less likely to be correct. The correct answer introduces a reason to doubt the reasoning or undermine the connection between the evidence and conclusion.',
 ARRAY[
   'Which one of the following, if true, most seriously weakens the argument?',
   'Which one of the following, if true, most undermines the conclusion drawn above?',
   'Which one of the following, if true, casts the most doubt on the argument?'
 ],
 '[{"title":"Find the conclusion","body":"Identify the main claim being argued before looking for weaknesses."},{"title":"Map the evidence","body":"Understand what premises are being used to support the conclusion."},{"title":"Find the assumption","body":"Identify the unstated assumption the argument needs to hold — attacking this assumption is the most common way to weaken."},{"title":"Predict the type of weakener","body":"Think about what kind of information would make the conclusion less likely before reading the choices."},{"title":"Evaluate each choice","body":"For each option, ask: if this were true, would the conclusion become less likely? Eliminate choices that are irrelevant, strengthen, or address a different issue."},{"title":"Avoid the trap of refutation","body":"The correct answer does not have to disprove the conclusion entirely — it just needs to make it less likely or less well-supported."}]',
 ARRAY[
   'Weaken answers attack the link between the evidence and the conclusion, not just the conclusion alone.',
   'If the argument involves a causal claim, look for an answer that introduces an alternative cause.',
   'For survey or study arguments, look for answers that question the representativeness or methodology of the study.',
   'Weaken EXCEPT questions require you to find the one choice that does not weaken the argument.'
 ],
 '[{"stimulus":"A nutritionist argues that people who eat breakfast every day have lower body mass indexes on average than people who skip breakfast. Therefore, eating breakfast causes people to maintain a healthier weight.","question_stem":"Which one of the following, if true, most seriously weakens the argument above?","choices":[{"label":"A","text":"Some people who eat breakfast every day are still classified as overweight."},{"label":"B","text":"People who maintain a healthy weight tend to engage in other healthy habits, such as regular exercise, that independently reduce body mass index."},{"label":"C","text":"The nutritionist has studied breakfast habits for over twenty years."},{"label":"D","text":"Skipping breakfast has been shown to increase hunger later in the day."},{"label":"E","text":"Body mass index is widely used as a measure of healthy weight by medical professionals."}],"correct_index":1,"explanation":"Choice (B) is correct. The argument assumes that breakfast consumption is the cause of lower body mass index, but (B) introduces an alternative explanation — that people with lower body mass indexes also tend to exercise more and engage in other healthy behaviors. This breaks the causal link the argument relies on, significantly weakening the conclusion."}]',
 2),

-- 3. Assumption
('logical_reasoning', 'assumption',
 'Assumption questions ask you to identify an unstated premise that the argument requires in order for the conclusion to follow from the evidence. The correct answer is something the author must believe to be true even though it is never stated.',
 ARRAY[
   'The argument above assumes which one of the following?',
   'Which one of the following is an assumption required by the argument?',
   'The argument relies on which one of the following assumptions?'
 ],
 '[{"title":"Find the conclusion and evidence","body":"Identify the conclusion (what the author is trying to prove) and the evidence (what they use to prove it)."},{"title":"Find the gap","body":"Look for any logical jump between the evidence and the conclusion — the assumption will bridge this gap."},{"title":"Use the Negation Test","body":"Negate your candidate answer. If the negated version destroys the argument, the original is a required assumption."},{"title":"Watch for scope shifts","body":"Many assumptions involve a shift in language or scope between the evidence and conclusion. The correct answer closes this gap."},{"title":"Eliminate what is not needed","body":"Eliminate choices that the argument does not strictly require — even if true and plausible, they are not the assumption if the argument could work without them."},{"title":"Check for sufficiency vs. necessity","body":"For ''required assumption'' questions, the correct answer must be necessary — the argument cannot function if it is false."}]',
 ARRAY[
   'The Negation Test is your most reliable tool: negate the answer and see if the argument collapses.',
   'Scope shifts between evidence and conclusion are a strong signal for where the assumption lies.',
   'Required assumptions are necessary but not necessarily sufficient — the argument might still be weak even with the assumption true.',
   'Beware of choices that are too broad or introduce outside concepts not found in the argument.'
 ],
 '[{"stimulus":"Our company''s new software update reduced the average time employees spend on routine tasks by 15 percent. Therefore, employee productivity will increase by at least 15 percent over the next quarter.","question_stem":"The argument above assumes which one of the following?","choices":[{"label":"A","text":"Employees will use the time saved from routine tasks to perform additional productive work rather than taking breaks."},{"label":"B","text":"The software update did not introduce any new technical problems that slow down other tasks."},{"label":"C","text":"A 15 percent increase in productivity would be sufficient to meet the company''s quarterly goals."},{"label":"D","text":"The software update was appreciated by most employees who used it."},{"label":"E","text":"Routine tasks account for more than half of the work employees currently perform."}],"correct_index":0,"explanation":"Choice (A) is correct. The argument concludes that productivity will increase because routine task time decreased. But this only follows if employees actually use the freed-up time productively. If they use it for breaks or non-productive activities, there is no reason to expect a productivity gain. This gap between ''less time on routine tasks'' and ''more overall productivity'' is bridged by assumption (A)."}]',
 3),

-- 4. Flaw
('logical_reasoning', 'flaw',
 'Flaw questions ask you to identify the error in reasoning that an argument commits. Rather than attacking the conclusion, you are diagnosing what is logically wrong with how the argument is constructed.',
 ARRAY[
   'The reasoning in the argument above is flawed because the argument',
   'The argument is most vulnerable to criticism on the grounds that it',
   'Which one of the following most accurately describes a flaw in the reasoning above?'
 ],
 '[{"title":"Read for structure, not content","body":"Focus on how the argument works, not whether the facts are true. You are looking for a logical error in the structure."},{"title":"Find the conclusion and evidence","body":"Identify what is being concluded and what is being used to support it."},{"title":"Name the flaw type","body":"Mentally label the type of error: circular reasoning, ad hominem, false dichotomy, hasty generalization, confusing correlation with causation, equivocation, etc."},{"title":"Match the flaw to a choice","body":"Look for the answer choice that describes the error you identified, often in abstract language like ''takes for granted'' or ''fails to consider.''"},{"title":"Eliminate descriptions that are accurate","body":"Eliminate choices that describe the argument correctly — you want what is wrong, not what is right."},{"title":"Watch for answer choices that describe a real flaw not committed","body":"Some wrong answers describe real logical flaws, just not the one this argument makes. Stay focused on what this specific argument does wrong."}]',
 ARRAY[
   'Common flaw categories: ad hominem, circular reasoning, false dichotomy, hasty generalization, correlation/causation confusion, equivocation, part-to-whole, and appeal to authority.',
   'Flaw answer choices are often phrased abstractly — translate them back to the argument to check if they fit.',
   'The correct answer will accurately describe what the argument does — not what it should have done instead.',
   'Flaw questions never ask you to strengthen or fix the argument — only to identify the error.'
 ],
 '[{"stimulus":"Every professional athlete I have read about trains for at least four hours per day. My neighbor trains for four hours per day. Therefore, my neighbor must be a professional athlete.","question_stem":"The reasoning above is flawed because it","choices":[{"label":"A","text":"draws a conclusion about a specific individual from a generalization about a group"},{"label":"B","text":"assumes that a condition sufficient to belong to a group is also necessary to belong to that group"},{"label":"C","text":"relies on a sample that may not be representative of professional athletes generally"},{"label":"D","text":"confuses a correlation between two variables with a causal relationship"},{"label":"E","text":"ignores the possibility that the neighbor''s training could lead to professional status in the future"}],"correct_index":1,"explanation":"Choice (B) is correct. The argument establishes that four hours of training per day is something all professional athletes do (a necessary condition for the conclusion), but then treats it as if it were sufficient to be a professional athlete. This is a classic confusion of necessary and sufficient conditions. Just because all professional athletes train four hours per day does not mean everyone who trains four hours per day is a professional athlete."}]',
 4),

-- 5. Must Be True
('logical_reasoning', 'must_be_true',
 'Must Be True questions present a set of statements and ask you to identify a conclusion that is guaranteed to follow from those statements. The correct answer must be true whenever all the given statements are true — no exceptions.',
 ARRAY[
   'If the statements above are true, which one of the following must also be true?',
   'Which one of the following can be properly concluded from the information above?',
   'Which one of the following follows logically from the statements above?'
 ],
 '[{"title":"Treat each statement as a given fact","body":"Do not question whether the statements are true — accept them all as true and work forward from there."},{"title":"Look for logical entailments","body":"Ask: given all these statements together, what cannot possibly be false?"},{"title":"Use formal logic for conditional statements","body":"Identify if-then structures and apply modus ponens (affirming the antecedent) or modus tollens (denying the consequent)."},{"title":"Combine statements","body":"Often the correct answer follows from combining two or more statements together, not from any single one."},{"title":"Test each choice against the statements","body":"For each answer choice, ask: is it possible for all the given statements to be true and this choice to be false? If yes, eliminate it."},{"title":"Avoid ''could be true'' choices","body":"The correct answer must be true — not just possible or likely. Eliminate choices that might be true under some interpretations."}]',
 ARRAY[
   'The bar is high — the correct answer must be true in every possible scenario where the premises hold.',
   'Must Be True is not about what is likely or reasonable — it is about logical necessity.',
   'Contrapositive reasoning: if ''all A are B'' is given, then ''not B implies not A'' also follows.',
   'If the stimulus contains conditional logic (if/then), look for answer choices that use modus ponens or the contrapositive.'
 ],
 '[{"stimulus":"All board members of the foundation are required to attend the annual gala. Whenever the annual gala is held outdoors, it is also broadcast on local television. This year, the foundation decided to hold the gala outdoors.","question_stem":"If the statements above are true, which one of the following must also be true?","choices":[{"label":"A","text":"All board members of the foundation will be on local television this year."},{"label":"B","text":"The annual gala has always been held outdoors."},{"label":"C","text":"More people will watch the gala this year than in previous years."},{"label":"D","text":"Board members who attend the gala prefer outdoor events."},{"label":"E","text":"The foundation will increase fundraising this year due to the television broadcast."}],"correct_index":0,"explanation":"Choice (A) must be true. From the premises: (1) all board members must attend the gala, and (2) when the gala is outdoors, it is broadcast on television. This year the gala is outdoors, so it will be broadcast. Since all board members must attend, all board members will appear on television. This follows necessarily from the given statements."}]',
 5),

-- 6. Most Strongly Supported
('logical_reasoning', 'most_strongly_supported',
 'Most Strongly Supported questions ask you to identify what the passage most supports or what can be most reasonably inferred. Unlike Must Be True, the correct answer is the best-supported conclusion, not necessarily a logically necessary one.',
 ARRAY[
   'Which one of the following is most strongly supported by the information above?',
   'The information above most supports which one of the following?',
   'Which one of the following can most reasonably be inferred from the passage above?'
 ],
 '[{"title":"Accept all statements as true","body":"Treat everything in the stimulus as established fact and reason from there."},{"title":"Look for the best-supported choice, not the certain one","body":"The correct answer is most strongly supported — it can be reasonably inferred, even if not guaranteed with 100% certainty."},{"title":"Stay tightly tied to the stimulus","body":"The correct answer will be directly supported by specific statements in the passage — avoid choices that go far beyond the given information."},{"title":"Rank the choices","body":"After eliminating clearly wrong choices, compare survivors to see which is most directly and strongly supported."},{"title":"Watch for extreme language","body":"Choices using words like ''always,'' ''never,'' or ''all'' are often too strong to be well-supported. Choices using ''some,'' ''most,'' or ''likely'' are often safer."},{"title":"Distinguish from Must Be True","body":"Most Strongly Supported requires a high degree of support but not logical certainty. The bar is still high — pick what the evidence most clearly points to."}]',
 ARRAY[
   'The bar is strong support, not logical certainty — the correct answer can be reasonably inferred from the statements.',
   'Avoid choices that introduce concepts not mentioned or strongly implied by the stimulus.',
   'Moderate language (''likely,'' ''suggests,'' ''probably'') often signals a supportable conclusion.',
   'Compare answer choices and pick the one with the strongest direct backing from the passage.'
 ],
 '[{"stimulus":"A survey of 2,000 adults found that those who reported sleeping fewer than six hours per night also reported higher levels of daily stress than those who slept seven or more hours. The survey also found that adults with high daily stress were significantly more likely to report difficulty falling asleep.","question_stem":"The information above most strongly supports which one of the following?","choices":[{"label":"A","text":"Lack of sleep is the primary cause of stress in adults."},{"label":"B","text":"Stress and poor sleep may reinforce each other in a cyclical pattern."},{"label":"C","text":"All adults who sleep fewer than six hours per night experience clinical stress disorders."},{"label":"D","text":"Sleeping more than eight hours per night eliminates stress entirely."},{"label":"E","text":"The survey results apply equally to children and teenagers."}],"correct_index":1,"explanation":"Choice (B) is most strongly supported. The survey found that poor sleep correlates with high stress, and high stress correlates with difficulty sleeping. Together these findings suggest a mutually reinforcing cycle. Choices (A), (C), and (D) go far beyond the evidence by making absolute or causal claims the survey cannot support. Choice (E) is not supported since the survey only covered adults."}]',
 6),

-- 7. Method of Reasoning
('logical_reasoning', 'method_of_reasoning',
 'Method of Reasoning questions ask you to describe how an argument proceeds — what logical technique or argumentative strategy the author uses to reach their conclusion. You are not evaluating whether the argument is good or bad, just describing its structure.',
 ARRAY[
   'The argument proceeds by',
   'The reasoning in the argument above employs which one of the following techniques?',
   'The author''s conclusion follows because the author'
 ],
 '[{"title":"Read for structure, not content","body":"Focus on the logical moves the argument makes rather than the specific topic. Ask: what technique is being used?"},{"title":"Identify common structural patterns","body":"Look for patterns like: citing an analogy, drawing a counterexample, appealing to authority, making a general rule from specific cases, or applying a general rule to a specific case."},{"title":"Translate the structure into abstract terms","body":"Practice summarizing the argument''s structure using neutral placeholder terms (A, B, principle, etc.) before reading the answer choices."},{"title":"Match your abstract description to a choice","body":"Answer choices for Method questions often use abstract language. Match the language to the structure you identified."},{"title":"Eliminate choices that describe what the argument does not do","body":"If the argument does not cite a counter-argument, eliminate choices that say it ''refutes an objection.'' Stay precise."},{"title":"Watch for answer choices that describe the conclusion rather than the method","body":"Some wrong answers describe the conclusion of the argument, not how the argument reaches it."}]',
 ARRAY[
   'Common methods: analogy, counterexample, general principle applied to specific case, appeal to consequences, citing evidence, eliminating alternatives.',
   'Method questions describe the structure of the argument — they do not ask you to evaluate whether the argument is good.',
   'Translate the argument into abstract structural terms before reading the answer choices.',
   'Eliminate answer choices that describe a logical move the argument never makes.'
 ],
 '[{"stimulus":"Allowing students to use calculators during math exams does not constitute cheating because calculators are just tools that assist computation, and no one considers it cheating to use a pencil rather than a pen. Therefore, using a calculator is no different from using any other permitted writing tool.","question_stem":"The argument proceeds by","choices":[{"label":"A","text":"appealing to the authority of educators to define what constitutes cheating"},{"label":"B","text":"drawing an analogy between calculator use and an already-accepted practice to argue they should be treated the same way"},{"label":"C","text":"providing statistical evidence that calculator use does not affect exam outcomes"},{"label":"D","text":"refuting the strongest objection to allowing calculators before drawing a conclusion"},{"label":"E","text":"defining a key term and then deriving logical consequences from that definition"}],"correct_index":1,"explanation":"Choice (B) is correct. The argument compares using a calculator to using a pencil instead of a pen — an already accepted practice. By drawing this analogy, the argument concludes that calculators should also be considered acceptable. This is a classic analogical reasoning structure."}]',
 7),

-- 8. Parallel Reasoning
('logical_reasoning', 'parallel_reasoning',
 'Parallel Reasoning questions ask you to find the answer choice whose argument has the same logical structure as the argument in the stimulus. The content will be completely different, but the form of the reasoning must match exactly.',
 ARRAY[
   'Which one of the following arguments is most similar in its reasoning to the argument above?',
   'The pattern of reasoning in the argument above is most similar to that in which one of the following?',
   'Which one of the following most closely parallels the reasoning used in the argument above?'
 ],
 '[{"title":"Abstract the stimulus argument","body":"Identify the conclusion and premises of the stimulus argument, then restate them using placeholder letters or neutral terms (all A are B; this is an A; therefore this is a B)."},{"title":"Note the argument type","body":"Categorize the argument: deductive, inductive, analogical, causal, etc. Valid or invalid? The parallel must match on this dimension too."},{"title":"Check the validity structure","body":"If the original is a valid argument, the parallel must also be valid. If the original is flawed, the parallel must commit the same flaw."},{"title":"Eliminate by mismatching structure","body":"Go through each choice and abstract its structure the same way you did the stimulus. Quickly eliminate any that have a different number of premises, a different conclusion type, or a different logical move."},{"title":"Match every element","body":"The parallel must match: number of premises, type of inference, whether the conclusion is affirmative or negative, and whether the argument is valid or flawed."},{"title":"Confirm the best match","body":"Once you have narrowed to one or two candidates, diagram both carefully against the stimulus to confirm the structure is identical."}]',
 ARRAY[
   'Abstract the argument to its bare logical form before reading the choices — this prevents content from distracting you.',
   'Valid arguments must be paralleled by valid arguments; flawed arguments must be paralleled by the same flaw.',
   'The number and type of premises must match exactly.',
   'Content is irrelevant — a correct parallel can be about any topic as long as the logical structure matches.'
 ],
 '[{"stimulus":"All members of the planning committee must approve any budget change. Councilmember Hayes is not a member of the planning committee. Therefore, Councilmember Hayes''s approval is not required for budget changes.","question_stem":"The pattern of reasoning in the argument above is most similar to that in which one of the following?","choices":[{"label":"A","text":"Everyone who completes the certification program is eligible for promotion. Jana completed the program. Therefore, Jana is eligible for promotion."},{"label":"B","text":"Only licensed contractors may perform electrical work in the building. Miguel is not a licensed contractor. Therefore, Miguel may not perform electrical work in the building."},{"label":"C","text":"All students who pass the entrance exam are admitted to the program. Leon did not pass the entrance exam. Therefore, Leon was not admitted to the program."},{"label":"D","text":"Membership in the club requires payment of annual dues. Reyes paid the dues. Therefore, Reyes is a member of the club."},{"label":"E","text":"Attending the conference is optional for all employees except managers. Kim is not a manager. Therefore, Kim may choose not to attend the conference."}],"correct_index":2,"explanation":"Choice (C) is correct. The stimulus structure is: All X must do Y; Z is not an X; therefore Y does not apply to Z (denying the antecedent). Choice (C) mirrors this exactly: All who pass are admitted; Leon did not pass; therefore Leon was not admitted. Note this is actually a formal fallacy (denying the antecedent) in both cases — and the parallel must match the flaw, not correct it."}]',
 8),

-- 9. Parallel Flaw
('logical_reasoning', 'parallel_flaw',
 'Parallel Flaw questions ask you to find the answer choice that commits the same logical error as the argument in the stimulus. The flaw must match in type and structure, even though the subject matter will be completely different.',
 ARRAY[
   'Which one of the following arguments contains a flaw that is most similar to the flaw in the argument above?',
   'The flawed pattern of reasoning in the argument above is most similar to that in which one of the following?',
   'Which one of the following exhibits the same flawed reasoning as the argument above?'
 ],
 '[{"title":"Identify the flaw first","body":"Before reading the answer choices, diagnose the specific logical error in the stimulus argument. Name it clearly."},{"title":"Abstract the flaw structure","body":"Translate the flaw into abstract terms. For example: ''treats a necessary condition as if it were sufficient'' or ''assumes that because A preceded B, A caused B.''"},{"title":"Match the flaw type and structure","body":"Each answer choice must be evaluated not just for whether it contains a flaw, but whether it contains the same flaw as the stimulus."},{"title":"Ignore content similarity","body":"The correct answer may be about a completely unrelated topic. Focus entirely on the logical structure."},{"title":"Eliminate choices with different flaws","body":"Many wrong answers contain flaws, just not the same flaw. Be precise about what type of error you are looking for."},{"title":"Diagram if needed","body":"For complex flaws involving conditionals, write out the logical structure of the stimulus and each answer choice side by side."}]',
 ARRAY[
   'Identify and name the specific flaw in the stimulus before reading the choices.',
   'The parallel must commit the same flaw, not just any flaw — precision is essential.',
   'Content is irrelevant; focus exclusively on the logical structure of the error.',
   'Common parallel flaws: affirming the consequent, denying the antecedent, false cause, part-whole errors, hasty generalization.'
 ],
 '[{"stimulus":"If the project is completed on time, the client will be satisfied. The client is satisfied. Therefore, the project was completed on time.","question_stem":"The flawed pattern of reasoning in the argument above is most similar to that in which one of the following?","choices":[{"label":"A","text":"If it rains, the game will be cancelled. The game was cancelled. Therefore, it rained."},{"label":"B","text":"If Maria studies, she will pass the exam. Maria did not study. Therefore, she did not pass the exam."},{"label":"C","text":"If the engine overheats, the car will stall. The engine did not overheat. Therefore, the car will not stall."},{"label":"D","text":"All dogs are mammals. Rex is a mammal. Therefore, Rex is a dog."},{"label":"E","text":"If demand increases, prices will rise. Prices have risen. Therefore, demand must have increased."}],"correct_index":0,"explanation":"The stimulus commits the fallacy of affirming the consequent: If P then Q; Q; therefore P. Choice (A) has the same structure: If rain then cancellation; cancellation; therefore rain. This is affirming the consequent. Choice (D) also commits affirming the consequent but using universal statements rather than conditionals, making (A) the closer structural parallel to the stimulus."}]',
 9),

-- 10. Principle
('logical_reasoning', 'principle',
 'Principle questions ask you to identify a general rule, norm, or standard that the argument either relies on, illustrates, or should follow. The correct answer is a broad principle that, when applied, supports or explains the conclusion in the argument.',
 ARRAY[
   'Which one of the following principles, if valid, most helps to justify the reasoning above?',
   'The argument above most closely conforms to which one of the following principles?',
   'Which one of the following principles most helps to explain the reasoning in the argument?'
 ],
 '[{"title":"Find the conclusion and evidence","body":"Identify exactly what conclusion is being drawn and what evidence is offered for it."},{"title":"Ask what general rule would validate the move","body":"Think: what kind of general principle, if true, would bridge the evidence to the conclusion and make the reasoning legitimate?"},{"title":"Look for abstract generalizations","body":"Correct answers are typically phrased as broad, general rules (''Whenever X, Y'' or ''Actions that do Z are permissible if...'') rather than specific statements about this situation."},{"title":"Apply the principle to the argument","body":"Plug your candidate principle back into the argument and check: does it actually bridge the gap and support the conclusion?"},{"title":"Eliminate overly specific choices","body":"Eliminate choices that describe only the specific situation in the stimulus rather than offering a general rule."},{"title":"Watch for scope","body":"The principle must be broad enough to apply to the situation but not so broad that it justifies unintended conclusions."}]',
 ARRAY[
   'Correct principles are general rules — not descriptions of the specific situation in the argument.',
   'Apply each candidate principle back to the argument to confirm it actually bridges the gap.',
   'A principle that justifies the reasoning must make the conclusion follow from the evidence.',
   'Distinguish ''Principle'' questions (identify the rule) from ''Apply the Principle'' questions (use a given rule).'
 ],
 '[{"stimulus":"A pharmaceutical company discovered a safety issue with one of its products only after the product had been on the market for two years. Although the issue was minor and no consumers were harmed, the company immediately issued a voluntary recall. The company''s decision was clearly the right one.","question_stem":"Which one of the following principles, if valid, most helps to justify the reasoning above?","choices":[{"label":"A","text":"Companies that cause harm to consumers should be held legally responsible for their actions."},{"label":"B","text":"A company acts correctly when it takes proactive steps to protect consumer safety even when no harm has yet occurred."},{"label":"C","text":"Pharmaceutical products should undergo more rigorous safety testing before they are approved for sale."},{"label":"D","text":"Voluntary recalls are more effective than government-mandated recalls at preventing consumer harm."},{"label":"E","text":"Companies should be transparent about safety issues only when required to do so by law."}],"correct_index":1,"explanation":"Choice (B) is correct. The argument concludes that the recall was the right decision even though no harm occurred. The principle in (B) — that proactively protecting consumers is correct even before harm happens — directly justifies this conclusion. The other choices either address legal liability, regulatory policy, or transparency requirements that go beyond the argument''s scope."}]',
 10),

-- 11. Apply Principle
('logical_reasoning', 'apply_principle',
 'Apply the Principle questions give you a general rule or principle and ask you to identify which answer choice correctly applies that principle to a new situation. You must determine which scenario is governed by the given rule.',
 ARRAY[
   'Which one of the following best illustrates the principle stated above?',
   'Which one of the following is an example of the principle described above?',
   'According to the principle stated above, which one of the following is most analogous to the situation described?'
 ],
 '[{"title":"Understand the principle fully","body":"Before reading the answer choices, make sure you completely understand every element of the given principle. Identify its conditions and consequences."},{"title":"Translate the principle into specific terms","body":"Rephrase the general rule in your own words. What exactly must be true for the principle to apply? What conclusion does it yield?"},{"title":"Apply the principle to each choice","body":"Work through each answer choice and check whether the conditions of the principle are met and whether the stated conclusion matches what the principle would predict."},{"title":"Look for a perfect match","body":"The correct answer will satisfy all conditions of the principle and draw the same conclusion the principle requires."},{"title":"Eliminate partial matches","body":"Wrong answers often satisfy some but not all elements of the principle, or they draw a different conclusion than the principle would support."},{"title":"Do not rely on intuition","body":"Apply the principle mechanically — the correct answer may describe a situation you personally disagree with but that correctly illustrates the rule."}]',
 ARRAY[
   'Work through the principle''s conditions systematically before reading the choices.',
   'All conditions of the principle must be satisfied — a partial match is a wrong answer.',
   'The conclusion in the correct answer must also match what the principle requires.',
   'Apply the rule mechanically — your personal views about the content are irrelevant.'
 ],
 '[{"stimulus":"Principle: A sports league should suspend a player for unsportsmanlike conduct if the conduct occurred during an official game and the conduct was witnessed by a game official.","question_stem":"Which one of the following best illustrates this principle?","choices":[{"label":"A","text":"A player who taunted an opponent during a practice session is suspended after a teammate reports the behavior to the league."},{"label":"B","text":"A player who argued with a referee during an official game is suspended after the referee files an official report of the incident."},{"label":"C","text":"A player who damaged equipment in the locker room after a game is suspended following a league investigation."},{"label":"D","text":"A player who posted offensive comments on social media is suspended after the league reviews the posts."},{"label":"E","text":"A player who was rude to fans outside the stadium is suspended after video footage surfaces online."}],"correct_index":1,"explanation":"Choice (B) is correct. The principle requires: (1) unsportsmanlike conduct, (2) during an official game, and (3) witnessed by a game official. Choice (B) satisfies all three: taunting an opponent qualifies as unsportsmanlike, it happened during an official game, and the referee (a game official) witnessed it. Choices (A), (C), (D), and (E) each fail at least one condition — the conduct either occurred outside a game or was not witnessed by a game official."}]',
 11),

-- 12. Evaluate
('logical_reasoning', 'evaluate',
 'Evaluate questions ask you to identify a piece of information that would be most useful in assessing the strength of an argument. The correct answer is something that, depending on whether it is true or false, would either strengthen or weaken the argument.',
 ARRAY[
   'Which one of the following would it be most useful to determine in order to evaluate the argument?',
   'Which one of the following would be most helpful to know in evaluating the argument above?',
   'The answer to which one of the following questions would most help to evaluate the argument?'
 ],
 '[{"title":"Find the core claim or assumption","body":"Identify the key assumption the argument is making. The most useful information to evaluate will test whether that assumption holds."},{"title":"Apply the Bi-directional Test","body":"For each answer choice, ask: if the answer to this question is ''yes,'' does it affect the argument''s strength? If the answer is ''no,'' does it affect the argument? The correct answer should affect the argument in both directions."},{"title":"Look for the choice that most directly targets the gap","body":"The correct answer addresses the weakest or most critical link in the argument''s reasoning chain."},{"title":"Eliminate one-directional choices","body":"Choices that only strengthen (or only weaken) the argument regardless of the answer are less useful for evaluation than choices whose answer could go either way."},{"title":"Ignore irrelevant information","body":"Eliminate choices that address factors completely outside the scope of the argument''s reasoning."},{"title":"Think about methodology and assumptions","body":"For arguments based on studies or statistics, good evaluation questions often probe the study''s methodology, sample, or scope."}]',
 ARRAY[
   'The Bi-directional Test: the correct answer should have the potential to either strengthen or weaken depending on its truth value.',
   'Focus on the core assumption or weakest link in the argument''s chain of reasoning.',
   'Evaluate questions are about what is most useful to know — not what would definitely prove or disprove the conclusion.',
   'Choices that are irrelevant regardless of their answer can be quickly eliminated.'
 ],
 '[{"stimulus":"A city mayor proposed installing more streetlights in the downtown district to reduce crime. The city council supported the proposal, citing studies showing that well-lit areas tend to have lower crime rates than poorly lit areas. The council concluded that installing more streetlights will therefore lower the crime rate downtown.","question_stem":"Which one of the following would be most useful to determine in order to evaluate the argument above?","choices":[{"label":"A","text":"Whether the mayor has proposed other crime-reduction measures in the past"},{"label":"B","text":"Whether the downtown district currently has adequate funding for new infrastructure projects"},{"label":"C","text":"Whether the studies cited by the council controlled for other factors that influence crime rates, such as policing levels and population density"},{"label":"D","text":"Whether residents of the downtown district support the streetlight proposal"},{"label":"E","text":"Whether the city has installed streetlights in other districts over the past decade"}],"correct_index":2,"explanation":"Choice (C) is correct and passes the Bi-directional Test. If the studies did control for other factors, the correlation between lighting and crime is more meaningful and the argument is strengthened. If the studies did not control for those factors, the correlation may be spurious and the argument is weakened. The other choices address political, financial, or historical context that does not directly bear on whether the correlation the argument relies on is valid."}]',
 12),

-- 13. Main Conclusion
('logical_reasoning', 'main_conclusion',
 'Main Conclusion questions ask you to identify the primary claim the argument is trying to establish. The main conclusion is the statement that the rest of the argument is designed to support — it is where the argument is going, not how it gets there.',
 ARRAY[
   'Which one of the following most accurately expresses the main conclusion of the argument above?',
   'The main point of the argument above is that',
   'The argument''s conclusion is best expressed by which one of the following?'
 ],
 '[{"title":"Use conclusion indicator words","body":"Look for words like ''therefore,'' ''thus,'' ''hence,'' ''so,'' ''it follows that,'' and ''consequently'' — they often introduce the conclusion."},{"title":"Use premise indicator words","body":"Words like ''because,'' ''since,'' ''given that,'' ''as,'' and ''for'' typically introduce premises, not the conclusion."},{"title":"Apply the Why Test","body":"For each statement, ask: does this claim exist to support another claim, or do other claims exist to support this one? The one that other claims support is the conclusion."},{"title":"Watch for the sub-conclusion trap","body":"Some arguments contain intermediate conclusions — claims that are supported by some evidence and also support the main conclusion. These are premises relative to the main conclusion."},{"title":"The conclusion need not be at the end","body":"The main conclusion can appear anywhere — at the start, in the middle, or at the end of the stimulus. Do not assume the last sentence is the conclusion."},{"title":"Match the scope precisely","body":"The correct answer will capture the main conclusion exactly — not too broad, not too narrow, and without distorting the original claim."}]',
 ARRAY[
   'The conclusion is what the author most wants you to believe — everything else exists to get you there.',
   'Sub-conclusions are evidence for the main conclusion — do not mistake them for the final point.',
   'The main conclusion can appear anywhere in the argument — do not assume it is the last sentence.',
   'If you are unsure, ask: ''Why does the author say X?'' If the answer is to support Y, then Y is more likely the conclusion.'
 ],
 '[{"stimulus":"Research has shown that people who spend time in nature exhibit lower levels of the stress hormone cortisol. Since cortisol is associated with numerous health problems including heart disease and weakened immunity, spending time in nature likely promotes better overall health. Employers should therefore encourage their employees to take regular outdoor breaks during the workday.","question_stem":"Which one of the following most accurately expresses the main conclusion of the argument above?","choices":[{"label":"A","text":"People who spend time in nature have lower levels of cortisol than those who do not."},{"label":"B","text":"Cortisol is associated with serious health problems including heart disease and weakened immunity."},{"label":"C","text":"Employers should encourage employees to take regular outdoor breaks during the workday."},{"label":"D","text":"Spending time in nature reduces exposure to workplace stress."},{"label":"E","text":"Nature exposure is the most effective method for reducing employee cortisol levels."}],"correct_index":2,"explanation":"Choice (C) is the main conclusion. The argument builds from a research finding (nature lowers cortisol) through an intermediate conclusion (nature promotes health) to the final recommendation (employers should encourage outdoor breaks). Choices (A) and (B) are premises. Choice (C) is the ultimate point the author wants to establish — everything else in the argument exists to support this recommendation."}]',
 13),

-- 14. Role of Statement
('logical_reasoning', 'role_of_statement',
 'Role of Statement questions identify a specific claim within the argument and ask what logical function it performs. The correct answer accurately describes whether the highlighted statement is a main conclusion, a premise supporting the main conclusion, an intermediate conclusion, a background statement, or something else.',
 ARRAY[
   'The claim that [X] plays which one of the following roles in the argument?',
   'The statement that [X] functions in the argument as',
   'In the argument, the claim that [X] is'
 ],
 '[{"title":"Identify the main conclusion first","body":"Before analyzing the highlighted statement, determine the argument''s main conclusion. This gives you context for understanding every other statement''s role."},{"title":"Test whether the statement is supported or supporting","body":"Ask: do other statements support this claim, or does this claim support other statements? Premises support; conclusions are supported."},{"title":"Check for intermediate conclusions","body":"An intermediate conclusion is supported by some premises and itself supports the main conclusion. Recognizing this three-level structure is critical."},{"title":"Consider background context","body":"Some statements merely set the scene without directly supporting or being supported by the main argument. These are background or context-setting statements."},{"title":"Match the role description precisely","body":"Answer choices use precise language: ''a premise offered in support of,'' ''an intermediate conclusion,'' ''the main conclusion,'' ''a consideration against which,'' etc. Make sure every word of the description fits."},{"title":"Eliminate mismatched descriptions","body":"If a choice describes the statement as a premise but it is actually a conclusion, or vice versa, eliminate it immediately even if the content sounds right."}]',
 ARRAY[
   'Identify the main conclusion before analyzing any specific statement''s role.',
   'A statement can be both a conclusion (supported by premises) and a premise (supporting the main conclusion) — this is an intermediate conclusion.',
   'Background statements provide context but are not part of the logical chain of support.',
   'Precise language matters in the answer choices — match every word, not just the general category.'
 ],
 '[{"stimulus":"Studies show that children who read for pleasure perform better on standardized tests across all subjects. Reading for pleasure builds vocabulary and comprehension skills. Since vocabulary and comprehension are foundational to learning in every subject, schools should dedicate more class time to independent reading.","question_stem":"In the argument, the claim that reading for pleasure builds vocabulary and comprehension skills plays which one of the following roles?","choices":[{"label":"A","text":"It is the main conclusion that the rest of the argument is designed to establish."},{"label":"B","text":"It is a premise that directly supports the main conclusion about school policy."},{"label":"C","text":"It is an intermediate conclusion supported by the research and itself supporting the argument''s final recommendation."},{"label":"D","text":"It is background information that contextualizes the research findings."},{"label":"E","text":"It is a counterargument that the author addresses before drawing the conclusion."}],"correct_index":2,"explanation":"Choice (C) is correct. The claim that reading builds vocabulary and comprehension is an intermediate conclusion. It is supported by the premise about test performance and the research findings, and it in turn supports the main conclusion that schools should dedicate more time to independent reading. It functions as a stepping stone between the evidence and the final recommendation, making it an intermediate conclusion rather than a main conclusion or a simple premise."}]',
 14);
