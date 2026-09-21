package engine

import (
	"github.com/felixzsh/botflota"
)

type routeResult struct {
	Target string
	Method botflota.RoutingMethod
}

func (e *Engine) route(r *run, created bool) (routeResult, error) {
	mode := e.graph.Routing
	if mode == "" {
		mode = "off"
	}
	if mode == "always" && e.hasLLM {
		target, ok, err := e.classify(r)
		if err != nil {
			return routeResult{}, err
		}
		if ok {
			return routeResult{Target: target, Method: botflota.RoutingSystemOne}, nil
		}
	}
	if target, method := e.matchTriggers(r); target != "" {
		return routeResult{Target: target, Method: method}, nil
	}
	if mode == "fallback" && e.hasLLM {
		target, ok, err := e.classify(r)
		if err != nil {
			return routeResult{}, err
		}
		if ok {
			return routeResult{Target: target, Method: botflota.RoutingSystemOne}, nil
		}
	}
	if created {
		return routeResult{Method: botflota.RoutingRoot}, nil
	}
	if target := e.defaultEdge(r.session.Node); target != "" {
		return routeResult{Target: target, Method: botflota.RoutingDefault}, nil
	}
	if e.graph.Fallback != "" {
		return routeResult{Target: e.graph.Fallback, Method: botflota.RoutingFallback}, nil
	}
	return routeResult{Method: botflota.RoutingUnknown}, nil
}

// Ties go to the most recent candidate.
func (e *Engine) matchTriggers(r *run) (string, botflota.RoutingMethod) {
	bestScore := 0.0
	bestIndex := -1
	bestTarget := ""
	bestMethod := botflota.RoutingMethod("")
	for index, nodeName := range candidates(r) {
		node, ok := e.graph.Nodes[nodeName]
		if !ok {
			continue
		}
		for _, edge := range node.Edges {
			if edge.Intent == "" || len(edge.Triggers) == 0 {
				continue
			}
			score, method := bestTriggerMatch(r.msg.Content.VisibleText(), edge.Triggers)
			if score < matchThreshold {
				continue
			}
			if score > bestScore || (score == bestScore && index > bestIndex) {
				bestScore, bestIndex = score, index
				bestTarget, bestMethod = edge.Goto, method
			}
		}
	}
	return bestTarget, bestMethod
}

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
	result, err := e.llm.Classify(r.ctx, botflota.ClassifyRequest{
		Goal:    e.graph.Nodes[r.session.Node].Goal,
		Intents: ordered,
		Text:    r.msg.Content.VisibleText(),
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
