package main
import ("fmt";"os";"tokenflow/internal/pn")
func main() {
	data, _ := os.ReadFile("examples/tokenflow/v1.json")
	p, _ := pn.ParseBundle(data)
	dl := p.Alts["m_deadlock"]
	fmt.Println("enabled at M0:", p.Net.EnabledTransitions(dl))
	res, _ := pn.Explore(p.Net, dl, p.Goal, pn.Config{Strategy: pn.StrategyBFS, Budget: 256, MaxCandidates: 6})
	fmt.Printf("verdict=%s states=%d edges=%d deadlocks=%d (depth %d)\n", res.Verdict, len(res.States), len(res.Edges), len(res.Deadlocks), res.Deadlocks[0].Depth)
}
