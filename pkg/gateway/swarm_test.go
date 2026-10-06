package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSubtasksPassesNewFields(t *testing.T) {
	raw := `[
		{"id":"a","agent_id":"main","task":"first","model":"llama3",
		 "tools":["web_search"],"timeout":"2m",
		 "requires":{"models":["llama3"],"skills":["search"]}},
		{"id":"b","agent_id":"writer","task":"second","depends_on":["a"]}
	]`
	subs := parseSubtasks(raw)
	require.Len(t, subs, 2)

	assert.Equal(t, "llama3", subs[0].Model)
	assert.Equal(t, []string{"web_search"}, subs[0].Tools)
	assert.Equal(t, 2*time.Minute, subs[0].Timeout)
	require.NotNil(t, subs[0].Requires)
	assert.Equal(t, []string{"llama3"}, subs[0].Requires.Models)
	assert.Equal(t, []string{"search"}, subs[0].Requires.Skills)

	// Sparse subtasks still parse; unknown-but-wellformed extras are ignored.
	assert.Equal(t, "writer", subs[1].AgentID)
	assert.Equal(t, []string{"a"}, subs[1].DependsOn)
	assert.Nil(t, subs[1].Requires)
	assert.Zero(t, subs[1].Timeout)
}
