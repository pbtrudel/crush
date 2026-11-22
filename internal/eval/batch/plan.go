package batch

import (
	"math/rand/v2"
	"sort"

	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/score"
	"github.com/charmbracelet/crush/internal/eval/task"
)

// plan returns the run keys of a batch in the order of design 3.6: the task
// order is shuffled by seed, then each task runs every repetition once,
// alternating the routes and the variants, so a provider that slows down
// during the batch affects every variant alike.
func plan(tasks []*task.Task, exp *score.Experiment, labels map[string]string, seed uint64) []result.Key {
	order := make([]string, len(tasks))
	for i, tk := range tasks {
		order[i] = tk.ID
	}
	sort.Strings(order)
	rng := rand.New(rand.NewPCG(seed, 0))
	rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

	keys := make([]result.Key, 0, len(tasks)*exp.Repetitions*len(exp.Routes)*len(exp.Variants))
	for _, id := range order {
		for rep := 1; rep <= exp.Repetitions; rep++ {
			for _, name := range exp.Routes {
				for _, variant := range exp.Variants {
					keys = append(keys, result.Key{Task: id, Variant: labels[variant.Name], Route: name, Rep: rep})
				}
			}
		}
	}
	return keys
}
