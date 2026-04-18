// Package quality provides code quality measurement utilities for RINSE:
// quality score computation, exponential decay fitting for convergence rate,
// and Copilot comment classification.
package quality

import (
	"math"
	"regexp"
	"strings"
)

// Category classifies a Copilot review comment.
type Category string

const (
	CategorySecurity     Category = "security"
	CategoryCorrectness  Category = "correctness"
	CategoryErrorHandling Category = "error_handling"
	CategoryStyle        Category = "style"
	CategoryUnknown      Category = "unknown"
)

// Weight returns the defect weight for scoring purposes.
func (c Category) Weight() float64 {
	switch c {
	case CategorySecurity:
		return 10
	case CategoryCorrectness:
		return 5
	case CategoryErrorHandling:
		return 3
	case CategoryStyle:
		return 1
	default:
		return 1
	}
}

// CategoryCounts records how many comments fell into each category.
type CategoryCounts struct {
	Security      int `json:"security"`
	Correctness   int `json:"correctness"`
	ErrorHandling int `json:"error_handling"`
	Style         int `json:"style"`
	Unknown       int `json:"unknown"`
}

// Total returns the sum of all category counts.
func (cc CategoryCounts) Total() int {
	return cc.Security + cc.Correctness + cc.ErrorHandling + cc.Style + cc.Unknown
}

// WeightedSum returns the weighted defect count.
func (cc CategoryCounts) WeightedSum() float64 {
	return float64(cc.Security)*CategorySecurity.Weight() +
		float64(cc.Correctness)*CategoryCorrectness.Weight() +
		float64(cc.ErrorHandling)*CategoryErrorHandling.Weight() +
		float64(cc.Style)*CategoryStyle.Weight() +
		float64(cc.Unknown)*CategoryUnknown.Weight()
}

// securityPatterns matches security-related comment text.
var securityPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bsql\s*injection\b`),
	regexp.MustCompile(`(?i)\bxss\b`),
	regexp.MustCompile(`(?i)\bcsrf\b`),
	regexp.MustCompile(`(?i)\bauth(?:entication|orization)?\b`),
	regexp.MustCompile(`(?i)\bsecret\b`),
	regexp.MustCompile(`(?i)\bpassword\b`),
	regexp.MustCompile(`(?i)\bcredential\b`),
	regexp.MustCompile(`(?i)\btoken\b`),
	regexp.MustCompile(`(?i)\bpath\s*traversal\b`),
	regexp.MustCompile(`(?i)\binjection\b`),
	regexp.MustCompile(`(?i)\binsecure\b`),
	regexp.MustCompile(`(?i)\bvulnerabilit\w+\b`),
	regexp.MustCompile(`(?i)\bencrypt\w*\b`),
	regexp.MustCompile(`(?i)\bsanitiz\w+\b`),
	regexp.MustCompile(`(?i)\bpermission\b`),
}

// correctnessPatterns matches logic/correctness-related comment text.
var correctnessPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bbug\b`),
	regexp.MustCompile(`(?i)\bincorrect\b`),
	regexp.MustCompile(`(?i)\bwrong\b`),
	regexp.MustCompile(`(?i)\boff.by.one\b`),
	regexp.MustCompile(`(?i)\bnull\s*pointer\b`),
	regexp.MustCompile(`(?i)\bnil\s*pointer\b`),
	regexp.MustCompile(`(?i)\brace\s*condition\b`),
	regexp.MustCompile(`(?i)\bdeadlock\b`),
	regexp.MustCompile(`(?i)\blocgi?c\b`),
	regexp.MustCompile(`(?i)\bpanic\b`),
	regexp.MustCompile(`(?i)\boverflow\b`),
	regexp.MustCompile(`(?i)\bundefined\s*behavior\b`),
	regexp.MustCompile(`(?i)\bmissing\s*check\b`),
	regexp.MustCompile(`(?i)\bshould\s*return\b`),
	regexp.MustCompile(`(?i)\balways\s*(true|false)\b`),
}

// errorHandlingPatterns matches error-handling-related comment text.
var errorHandlingPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\berror\s*(?:is\s*)?(?:not\s*)?(?:handled|checked|ignored|returned)\b`),
	regexp.MustCompile(`(?i)\bunhandled\s*error\b`),
	regexp.MustCompile(`(?i)\berr\s*(?:!=|==)\s*nil\b`),
	regexp.MustCompile(`(?i)\bignor(?:e|ing)\s*(?:the\s*)?error\b`),
	regexp.MustCompile(`(?i)\bmissing\s*error\b`),
	regexp.MustCompile(`(?i)\berror\s*handling\b`),
	regexp.MustCompile(`(?i)\bshould\s*check\s*(?:the\s*)?error\b`),
	regexp.MustCompile(`(?i)\bpanic\s*on\s*error\b`),
	regexp.MustCompile(`(?i)\bfatal\b`),
	regexp.MustCompile(`(?i)\brecov(?:er|ery)\b`),
}

// stylePatterns matches style/convention-related comment text.
var stylePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bnaming\b`),
	regexp.MustCompile(`(?i)\bformatting\b`),
	regexp.MustCompile(`(?i)\bindentation\b`),
	regexp.MustCompile(`(?i)\bconvention\b`),
	regexp.MustCompile(`(?i)\bstyle\b`),
	regexp.MustCompile(`(?i)\bcomment\s*(?:missing|needed|should)\b`),
	regexp.MustCompile(`(?i)\bundocumented\b`),
	regexp.MustCompile(`(?i)\bgodoc\b`),
	regexp.MustCompile(`(?i)\bunused\b`),
	regexp.MustCompile(`(?i)\bduplicate\b`),
	regexp.MustCompile(`(?i)\bredundant\b`),
	regexp.MustCompile(`(?i)\bmagic\s*(?:number|string)\b`),
	regexp.MustCompile(`(?i)\bhard.?code\w*\b`),
}

// ClassifyComment categorises a Copilot review comment body into a Category.
// It applies weighted pattern matching in priority order:
// security > correctness > error_handling > style > unknown.
func ClassifyComment(text string) Category {
	for _, re := range securityPatterns {
		if re.MatchString(text) {
			return CategorySecurity
		}
	}
	for _, re := range correctnessPatterns {
		if re.MatchString(text) {
			return CategoryCorrectness
		}
	}
	for _, re := range errorHandlingPatterns {
		if re.MatchString(text) {
			return CategoryErrorHandling
		}
	}
	for _, re := range stylePatterns {
		if re.MatchString(text) {
			return CategoryStyle
		}
	}
	// Fallback: any comment containing "error" defaults to error_handling
	if strings.Contains(strings.ToLower(text), "error") {
		return CategoryErrorHandling
	}
	return CategoryUnknown
}

// ComputeQualityScore computes a quality score in [0, 1] from comment category
// counts. The formula is:
//
//	QS = 1 - (weightedDefects / maxPossibleDefects)
//
// where maxPossibleDefects uses the highest-weight category scaled to total
// comments (worst-case scenario: every comment is a security issue).
//
// Returns 1.0 (perfect) when there are no comments.
func ComputeQualityScore(cc CategoryCounts) float64 {
	total := cc.Total()
	if total == 0 {
		return 1.0
	}
	weighted := cc.WeightedSum()
	// Worst case: all comments are security defects (weight=10).
	maxPossible := float64(total) * CategorySecurity.Weight()
	score := 1.0 - (weighted / maxPossible)
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score
}

// FitExponentialDecay fits an exponential decay model to comment counts across
// iterations:
//
//	comments(n) = C₀ × e^(-λ×n)
//
// It returns λ (the fix rate). Higher λ means RINSE resolves comments faster
// per iteration. Returns 0 when there are fewer than 2 data points or the
// initial comment count is zero.
func FitExponentialDecay(commentsByIteration []int) float64 {
	if len(commentsByIteration) < 2 {
		return 0
	}
	c0 := float64(commentsByIteration[0])
	if c0 == 0 {
		return 0
	}

	// Use least-squares on the linearised form: ln(y) = ln(C0) - λ×n
	// For robustness, skip iterations where comments == 0 (perfectly resolved).
	n := len(commentsByIteration)
	var sumN, sumLnY, sumN2, sumNLnY float64
	count := 0
	for i, c := range commentsByIteration {
		if c <= 0 {
			continue
		}
		xi := float64(i)
		yi := math.Log(float64(c))
		sumN += xi
		sumLnY += yi
		sumN2 += xi * xi
		sumNLnY += xi * yi
		count++
	}

	if count < 2 {
		// Not enough non-zero points; use a simple two-point estimate.
		cLast := float64(commentsByIteration[n-1])
		if cLast <= 0 {
			cLast = 0.5 // treat "resolved" as half a comment
		}
		return math.Log(c0/cLast) / float64(n-1)
	}

	// OLS slope: λ = -(n*ΣxY - Σx*ΣY) / (n*Σx² - (Σx)²)
	denom := float64(count)*sumN2 - sumN*sumN
	if denom == 0 {
		return 0
	}
	slope := (float64(count)*sumNLnY - sumN*sumLnY) / denom
	lambda := -slope
	if lambda < 0 {
		lambda = 0
	}
	return lambda
}

// ResolutionRate computes R = 1 - (finalComments / initialComments).
// Returns 1.0 when all comments were resolved, 0.0 when none were resolved.
func ResolutionRate(initial, final int) float64 {
	if initial == 0 {
		return 1.0
	}
	r := 1.0 - float64(final)/float64(initial)
	if r < 0 {
		r = 0
	}
	return r
}

// QualityDelta holds the before/after quality metrics for a single PR cycle.
type QualityDelta struct {
	// CommentsBefore is the comment count on the first iteration.
	CommentsBefore int `json:"comments_before"`
	// CommentsAfter is the comment count on the final iteration (0 = all resolved).
	CommentsAfter int `json:"comments_after"`
	// CategoryBefore holds per-category comment counts at the start.
	CategoryBefore CategoryCounts `json:"category_before,omitempty"`
	// CategoryAfter holds per-category comment counts at the end.
	CategoryAfter CategoryCounts `json:"category_after,omitempty"`
	// ScoreBefore is the quality score before RINSE ran.
	ScoreBefore float64 `json:"score_before"`
	// ScoreAfter is the quality score after RINSE completed.
	ScoreAfter float64 `json:"score_after"`
	// FixRateLambda is the exponential decay constant λ.
	FixRateLambda float64 `json:"fix_rate_lambda"`
	// ResolutionRate is R = 1 - final/initial.
	ResolutionRate float64 `json:"resolution_rate"`
}

// Compute fills a QualityDelta from the per-iteration comment counts.
// commentsByIteration should be the slice of comment counts per iteration;
// categories are optional (pass nil/empty to skip per-category scoring).
func Compute(commentsByIteration []int, catBefore, catAfter CategoryCounts) QualityDelta {
	var d QualityDelta
	if len(commentsByIteration) == 0 {
		return d
	}
	d.CommentsBefore = commentsByIteration[0]
	d.CommentsAfter = commentsByIteration[len(commentsByIteration)-1]
	d.CategoryBefore = catBefore
	d.CategoryAfter = catAfter

	// Scores: if category data is available use it; else derive from comment counts.
	if catBefore.Total() > 0 {
		d.ScoreBefore = ComputeQualityScore(catBefore)
	} else {
		// Treat all as style (lowest weight) for a conservative estimate.
		d.ScoreBefore = ComputeQualityScore(CategoryCounts{Style: d.CommentsBefore})
	}
	if catAfter.Total() > 0 {
		d.ScoreAfter = ComputeQualityScore(catAfter)
	} else {
		d.ScoreAfter = ComputeQualityScore(CategoryCounts{Style: d.CommentsAfter})
	}

	d.FixRateLambda = FitExponentialDecay(commentsByIteration)
	d.ResolutionRate = ResolutionRate(d.CommentsBefore, d.CommentsAfter)
	return d
}

// ScoreDelta returns the improvement in quality score (positive = improvement).
func (d QualityDelta) ScoreDelta() float64 {
	return d.ScoreAfter - d.ScoreBefore
}
