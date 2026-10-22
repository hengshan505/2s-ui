package cronjob

import "github.com/shenaba/2s-ui/service"

// ClientRateJob samples per-client throughput on a shorter tick than the stats
// flush, which stays at 10s for the database's sake. Sampling reads the core's
// counters without emptying them, so the two never compete for the same bytes.
// It only samples while a tab is watching: only an open panel shows them.
type ClientRateJob struct{}

func NewClientRateJob() *ClientRateJob {
	return &ClientRateJob{}
}

func (j *ClientRateJob) Run() {
	service.TickClientRates()
}
