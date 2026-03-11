package sshmuxer

import (
	"log"
	"time"

	"github.com/antoniomika/sish/utils"
	"github.com/spf13/viper"
)

// startStrictIDCensedConnectionEnforcer disconnects active forwarded clients when
// their explicit connection ID is no longer present in the refreshed census cache.
func startStrictIDCensedConnectionEnforcer(state *utils.State) {
	if !viper.GetBool("census-enabled") {
		return
	}

	go func() {
		lastSeenRefresh := time.Time{}
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			if !utils.IsStrictIDCensedEnabled() {
				continue
			}

			snapshot := utils.GetCensusCacheSnapshot()
			if snapshot.LastRefresh.IsZero() || !snapshot.LastRefresh.After(lastSeenRefresh) {
				continue
			}

			lastSeenRefresh = snapshot.LastRefresh
			closed := 0

			state.SSHConnections.Range(func(_ string, sshConn *utils.SSHConnection) bool {
				if sshConn == nil {
					return true
				}

				if !sshConn.ConnectionIDProvided {
					return true
				}

				// Only enforce for active forwarded connections.
				if sshConn.ListenerCount() <= 0 {
					return true
				}

				if utils.IsIDCensed(sshConn.ConnectionID) {
					return true
				}

				sshConn.SendMessage("Forwarded id is not censed.", true)
				time.Sleep(1 * time.Millisecond)
				sshConn.CleanUp(state)
				closed++

				return true
			})

			if closed > 0 {
				log.Printf("Strict census enforcement closed %d connection(s) with non-censed IDs", closed)
			}
		}
	}()
}
