package conversation

import (
	"sort"

	"github.com/cedrx/chatd/internal/ipc"
)

// sortUsers orders by online-first then name ascending. Online peers
// float to the top of the dashboard; ties break alphabetically.
func sortUsers(rows []ipc.UserRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Online != rows[j].Online {
			return rows[i].Online && !rows[j].Online
		}
		return rows[i].Name < rows[j].Name
	})
}
