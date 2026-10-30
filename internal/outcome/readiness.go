package outcome

import "fmt"

// ReadinessNotApprovedError is an owning gate's durable manual decision, not
// model prose or a generic tool refusal. It stops this invocation without
// qualifying work, changing the brief or authorizing a claim.
type ReadinessNotApprovedError struct {
	Store, ApproachID, Snapshot string
}

func (e *ReadinessNotApprovedError) Error() string {
	return fmt.Sprintf("manual readiness approval was not granted for approach %q (snapshot %s); execution remains unadmitted; resume with approval of the current brief", e.ApproachID, e.Snapshot)
}
