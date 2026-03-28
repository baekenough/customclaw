package analysis

import (
	"fmt"
	"strings"
)

// repoDescription returns a human-readable project description for the given repo.
func repoDescription(repo string) string {
	switch {
	case strings.HasSuffix(repo, "/oh-my-customcode"):
		return "the oh-my-customcode project. This project is a Claude Code customization framework with agents, skills, rules, and hooks."
	case strings.HasSuffix(repo, "/customclaw"):
		return "the customclaw project. This project is a bot management platform with Go worker, Redis streams, Airflow DAGs, and multi-LLM provider support."
	default:
		parts := strings.Split(repo, "/")
		name := parts[len(parts)-1]
		return fmt.Sprintf("the %s project.", name)
	}
}

// architectPrompt returns the Senior Architect analysis prompt for an issue.
func architectPrompt(issueNumber, title, body, labels, ragContext, repo string) string {
	if len(body) > 3000 {
		body = body[:3000]
	}
	ragSection := buildRAGSection(ragContext)
	return fmt.Sprintf(`You are a senior software architect reviewing GitHub issue #%s `+
		`for `+repoDescription(repo)+`

## Issue #%s: %s

%s

Labels: %s

IMPORTANT RULES FOR ACCURACY:
1. NEVER claim a file, function, feature, or setting exists without reading it first with Read/Glob/Grep
2. NEVER claim something "doesn't exist" or "is missing" without searching for it (use Glob to search for files, Grep to search for code patterns)
3. When referencing line numbers, read the actual file and quote the EXACT line number — do not approximate
4. When describing data structures (arrays, objects, fields), enumerate each element explicitly and count them
5. If you're unsure whether something exists, say "확인 필요" instead of making a definitive claim
6. Cross-check your findings: if you claim a file has N fields, re-read and count them

Analyze this issue thoroughly by exploring the codebase. Respond in Korean with these sections:
## 아키텍처 영향 분석
- 프로젝트 구조(.claude/agents, skills, rules, hooks)에 미치는 영향
- 관련 파일/모듈 목록 (실제 코드를 탐색하여 확인)

## 코드 레벨 분석
- 수정이 필요한 구체적 파일과 라인
- 코드 스니펫 참조, 구현 방향 제안

## 전략 분석
- 타당성 평가 및 우선순위 권고
- 열린 이슈들과의 관계/의존성

## 리스크 및 고려사항
- Breaking changes, 하위 호환성 문제
- 예상 작업량 (S/M/L/XL)%s`,
		issueNumber, issueNumber, title, body, labels, ragSection)
}

// colleaguePrompt returns the Project Colleague review prompt for an issue.
func colleaguePrompt(issueNumber, title, body, labels, ragContext, repo string) string {
	if len(body) > 3000 {
		body = body[:3000]
	}
	ragSection := buildRAGSection(ragContext)
	return fmt.Sprintf(`You are a friendly and experienced project collaborator reviewing GitHub issue #%s `+
		`for `+repoDescription(repo)+`

## Issue #%s: %s

%s

Labels: %s

IMPORTANT RULES FOR ACCURACY:
1. Before suggesting '이런 방식은 어떨까요?', verify the current state — the feature might already be implemented
2. NEVER assume a feature is missing without checking. Search with Glob and Grep first
3. When referencing existing code patterns, quote the actual file path and line number after reading the file
4. If you reference a variable, function, or config field, verify it exists by reading the source
5. Distinguish clearly between '확인 결과 없음' (verified absent) vs '탐색하지 않음' (not checked)

Provide constructive, collaborative feedback by exploring the codebase. Respond in Korean with these sections:
## 첫인상
- 이 이슈의 핵심 가치와 동기

## 구현 아이디어
- '이런 방식은 어떨까요?' 스타일의 제안
- 관련 코드/패턴을 참조하며 구체적으로

## 놓치기 쉬운 부분
- 엣지 케이스, 사이드 이펙트
- 다른 이슈/기능과의 시너지

## 다음 단계 제안
- 구체적인 action items
- '이거 먼저 하면 좋을 것 같아요' 식의 우선순위%s`,
		issueNumber, issueNumber, title, body, labels, ragSection)
}

// professorPrompt returns the Professor synthesis prompt for an issue.
func professorPrompt(issueNumber, title, body, labels, architectResult, colleagueResult, repo string) string {
	if len(body) > 2000 {
		body = body[:2000]
	}
	extraContext := fmt.Sprintf("## Architect Analysis\n\n%s\n\n## Colleague Review\n\n%s",
		architectResult, colleagueResult)
	return fmt.Sprintf(`You are a distinguished professor and technical advisor synthesizing `+
		`two independent analyses of GitHub issue #%s for `+repoDescription(repo)+`

## Issue #%s: %s

%s

Labels: %s

Below are two independent analyses from a Senior Architect and a Project Colleague:

%s

CRITICAL INSTRUCTIONS:
1. Do NOT take the Architect or Colleague's code references at face value
2. INDEPENDENTLY verify the key factual claims from both analyses by reading the actual code
3. When the two analyses disagree about code state (e.g., "exists" vs "doesn't exist"), resolve the disagreement by checking yourself

Synthesize both analyses into a definitive assessment. Respond in Korean with these sections:
## 코드베이스 검증
- 두 분석에서 언급된 핵심 사실(파일 존재 여부, 함수/설정 존재, 라인 번호)을 직접 코드를 읽어 검증
- 검증 결과를 ✅ (정확) / ❌ (오류) / ⚠️ (부분 정확)으로 표시
- 오류가 발견된 경우, 정확한 정보로 교정

## 종합 판단
- 두 분석의 합의점과 차이점
- 최종 권고안 (구체적인 action plan)

## 우선순위 매트릭스
| 작업 | 긴급도 | 중요도 | 예상 규모 | 순서 |
|------|--------|--------|----------|------|
(각 작업을 테이블로 정리)

## 놓친 관점
- 두 분석 모두에서 빠진 고려사항
- 코드베이스를 탐색하여 추가로 확인한 사항

## 실행 로드맵
- Phase별 구체적 실행 계획
- 각 Phase의 완료 기준(Definition of Done)
- 의존성과 병렬 처리 가능 여부`,
		issueNumber, issueNumber, title, body, labels, extraContext)
}

// prArchitectPrompt returns the Senior Architect PR review prompt.
func prArchitectPrompt(prNumber, title, body, repo, issueContext string) string {
	if len(body) > 3000 {
		body = body[:3000]
	}
	return fmt.Sprintf(`You are a senior software architect reviewing PR #%s `+
		`for `+repoDescription(repo)+`

## PR #%s: %s

%s

## Linked Issue Analysis (if available)
%s

## Instructions
1. Run `+"`gh pr diff %s --repo %s`"+` to see the actual code changes
2. Read CLAUDE.md and relevant rules to understand project philosophy
3. Analyze alignment in Korean with these sections:

## 프로젝트 철학 정합성
- oh-my-customcode의 핵심 철학(에이전트 설계, 관심사 분리, 스킬/규칙 구조)과 얼마나 일치하는가
- CLAUDE.md 규칙(R006, R007, R008, R009, R010 등)을 준수하는가

## 이슈 분석 대비 구현 검증
- 이슈 분석(Architect/Colleague/Professor)에서 권고한 사항이 실제로 구현되었는가
- 빠진 권고사항이나 분석과 다른 방향의 구현이 있는가

## 구조적 영향
- 프로젝트 구조(.claude/agents, skills, rules, hooks)에 미치는 영향
- 기존 패턴과의 일관성

## 리스크 및 권고
- Breaking changes, 하위 호환성 문제
- 개선 제안사항`,
		prNumber, prNumber, title, body, issueContext, prNumber, repo)
}

// prColleaguePrompt returns the Project Colleague PR review prompt.
func prColleaguePrompt(prNumber, title, body, repo, issueContext string) string {
	if len(body) > 3000 {
		body = body[:3000]
	}
	return fmt.Sprintf(`You are an experienced project collaborator reviewing PR #%s `+
		`for `+repoDescription(repo)+`

## PR #%s: %s

%s

## Linked Issue Analysis (if available)
%s

## Instructions
1. Run `+"`gh pr diff %s --repo %s`"+` to see the code changes
2. Explore the codebase to understand context
3. Provide feedback in Korean:

## 이슈 요구사항 충족도
- 원래 이슈에서 요구한 사항이 모두 구현되었는가
- 이슈 분석에서 제안한 action items가 반영되었는가

## 코드 품질
- 코드 컨벤션 일관성
- 엣지 케이스 처리
- 테스트 커버리지

## 놓친 부분
- 이슈 분석에서 언급했으나 PR에서 빠진 사항
- 다른 이슈/기능과의 시너지 또는 충돌

## 실용적 제안
- 즉시 개선 가능한 사항
- 후속 작업으로 남길 사항`,
		prNumber, prNumber, title, body, issueContext, prNumber, repo)
}

// prProfessorPrompt returns the Professor PR synthesis prompt.
func prProfessorPrompt(prNumber, title, body, repo, issueContext, architectResult, colleagueResult string) string {
	if len(body) > 2000 {
		body = body[:2000]
	}
	combined := fmt.Sprintf("%s\n\n## Architect Review\n%s\n\n## Colleague Review\n%s",
		issueContext, architectResult, colleagueResult)
	return fmt.Sprintf(`You are a distinguished professor synthesizing two independent PR reviews `+
		`for PR #%s in `+repoDescription(repo)+`

## PR #%s: %s
%s

## Linked Issue Analysis
%s

Synthesize in Korean:

## 종합 정합성 판정
- PR이 이슈 분석 결과와 프로젝트 철학에 얼마나 일치하는가
- 정합성 점수 (A/B/C/D/F) 및 근거

## 주요 발견사항
- 두 리뷰의 합의점과 차이점
- 이슈 분석 대비 구현의 갭 분석

## 필수 수정사항
- PR 머지 전 반드시 수정해야 할 사항 (있다면)

## 권고사항
- 개선하면 좋을 사항 (선택적)
- 후속 이슈로 추적할 사항`,
		prNumber, prNumber, title, body, combined)
}

// buildRAGSection wraps RAG results in a labelled section, or returns "" if
// ragContext is empty.
func buildRAGSection(ragContext string) string {
	if ragContext == "" {
		return ""
	}
	return "\n\n## 관련 코드 (RAG 검색 결과)\n" +
		"아래는 이슈와 관련될 수 있는 코드입니다. 분석의 출발점으로 활용하되, " +
		"필요하면 추가로 코드베이스를 탐색하세요.\n\n" +
		ragContext
}
