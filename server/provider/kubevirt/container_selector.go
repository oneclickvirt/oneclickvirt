package kubevirt

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Use the Deployment's real selector, including imported deployments whose
// pod labels differ from the platform's default app=<name> convention.
func deploymentPodSelector(output string) (string, error) {
	var deployment struct {
		Spec struct {
			Selector struct {
				MatchLabels      map[string]string `json:"matchLabels"`
				MatchExpressions []struct {
					Key      string   `json:"key"`
					Operator string   `json:"operator"`
					Values   []string `json:"values"`
				} `json:"matchExpressions"`
			} `json:"selector"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(output), &deployment); err != nil {
		return "", err
	}
	var selectors []string
	for key, value := range deployment.Spec.Selector.MatchLabels {
		selectors = append(selectors, key+"="+value)
	}
	for _, expression := range deployment.Spec.Selector.MatchExpressions {
		switch expression.Operator {
		case "In", "NotIn":
			if len(expression.Values) == 0 {
				return "", fmt.Errorf("empty selector values")
			}
			operator := "in"
			if expression.Operator == "NotIn" {
				operator = "notin"
			}
			selectors = append(selectors, expression.Key+" "+operator+" ("+strings.Join(expression.Values, ",")+")")
		case "Exists":
			selectors = append(selectors, expression.Key)
		case "DoesNotExist":
			selectors = append(selectors, "!"+expression.Key)
		default:
			return "", fmt.Errorf("unsupported pod selector operator")
		}
	}
	if len(selectors) == 0 {
		return "", fmt.Errorf("deployment pod selector is empty")
	}
	sort.Strings(selectors)
	return strings.Join(selectors, ","), nil
}
