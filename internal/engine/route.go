package engine

import (
	"github.com/felixzsh/botforje/internal/domain"
)

// route picks the next node for the incoming message. Candidates are the
// visited nodes with the current one last (ADR 0010); an explicit trigger
// match wins, then the LLM when the mode allows it, then the current node's
// default edge, then the graph fallback. On session creation only an explicit
// match transitions; otherwise the root's entry actions run (ADR 0010).
func (e *Engine) route(r *run, created bool) (string, error) {
	mode := e.graph.Routing
	if mode == "" {
		mode = "off"
	}
	if mode == "always" && e.hasLLM {
		target, ok, err := e.classify(r)
		if err != nil {
			return "", err
		}
		if ok {
			return target, nil
		}
	}
	if target := e.matchTriggers(r); target != "" {
		return target, nil
	}
	if mode == "fallback" && e.hasLLM {
		target, ok, err := e.classify(r)
		if err != nil {
			return "", err
		}
		if ok {
			return target, nil
		}
	}
	if created {
		return "", nil
	}
	if target := e.defaultEdge(r.session.Node); target != "" {
		return target, nil
	}
	return e.graph.Fallback, nil
}

// matchTriggers scores every trigger edge of every candidate and returns the
// best target. Ties go to the most recent candidate.
func (e *Engine) matchTriggers(r *run) string {
	bestScore := 0.0
	bestIndex := -1
	bestTarget := ""
	for index, nodeName := range candidates(r) {
		node, ok := e.graph.Nodes[nodeName]
		if !ok {
			continue
		}
		for _, edge := range node.Edges {
			if edge.Intent == "" || len(edge.Triggers) == 0 {
				continue
			}
			score := bestScoreFor(r.msg.Body, edge.Triggers)
			if score < matchThreshold {
				continue
			}
			if score > bestScore || (score == bestScore && index > bestIndex) {
				bestScore = score
				bestIndex = index
				bestTarget = edge.Goto
			}
		}
	}
	return bestTarget
}

// classify asks the LLM to pick an edge intent among the candidate nodes.
func (e *Engine) classify(r *run) (string, bool, error) {
	intents := map[string]string{}
	var ordered []string
	for _, nodeName := range candidates(r) {
		for _, edge := range e.graph.Nodes[nodeName].Edges {
			if edge.Intent == "" {
				continue
			}
			if _, seen := intents[edge.Intent]; !seen {
				intents[edge.Intent] = edge.Goto
				ordered = append(ordered, edge.Intent)
			}
		}
	}
	if len(ordered) == 0 {
		return "", false, nil
	}
	result, err := e.llm.Classify(r.ctx, domain.ClassifyRequest{
		Goal:    e.graph.Nodes[r.session.Node].Goal,
		Intents: ordered,
		Text:    r.msg.Body,
	})
	if err != nil {
		return "", false, err
	}
	if !result.OK {
		return "", false, nil
	}
	target, ok := intents[result.Intent]
	return target, ok, nil
}

func (e *Engine) defaultEdge(node string) string {
	for _, edge := range e.graph.Nodes[node].Edges {
		if edge.Intent == "" {
			return edge.Goto
		}
	}
	return ""
}

func candidates(r *run) []string {
	if len(r.session.VisitedNodes) == 0 {
		return []string{r.session.Node}
	}
	return r.session.VisitedNodes
}
