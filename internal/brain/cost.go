package brain

import "strings"

// Prices in USD per million tokens (input, output). Cache reads are billed at
// 10% of input and cache writes at 125%. Update these if pricing changes.
var prices = map[string][2]float64{
	"claude-haiku-5-5":  {0.10, 0.50},
	"claude-sonnet-5-5": {2, 10},
	"claude-opus-5-5":   {4, 20},
	"claude-opus-5":     {5, 25},
	"claude-opus-4-8":   {5, 25},
	"claude-fable-5-2":  {8, 40},
}

func estimateCost(model string, in, out, cacheRead, cacheWrite int64) float64 {
	p, ok := prices[model]
	if !ok {
		// Unknown model (e.g. a fallback): find the closest family, else assume Opus pricing.
		p = prices["claude-opus-5-5"]
		for name, v := range prices {
			if strings.HasPrefix(model, name) {
				p = v
				break
			}
		}
	}
	const m = 1_000_000
	return float64(in)*p[0]/m +
		float64(out)*p[1]/m +
		float64(cacheRead)*p[0]*0.10/m +
		float64(cacheWrite)*p[0]*1.25/m
}
