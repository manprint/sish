package utils

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/spf13/viper"
)

func buildBandwidthProfileForUser(user string) *UserBandwidthProfile {
	statsOnly := NewConnectionStatsProfile()

	if !viper.GetBool("user-bandwidth-limiter-enabled") {
		return statsOnly
	}

	cfg, ok := getAuthUserBandwidthConfig(strings.TrimSpace(user))
	if !ok {
		return statsOnly
	}

	return NewUserBandwidthProfile(cfg.UploadBps, cfg.DownloadBps, cfg.Burst)
}

func formatBandwidthProfileForLog(profile *UserBandwidthProfile) string {
	if profile == nil {
		return "nil"
	}

	return fmt.Sprintf("uploadBps=%d downloadBps=%d burst=%.2f", profile.UploadBytesPerSecond, profile.DownloadBytesPerSecond, profile.BurstFactor)
}

// ReconcileBandwidthProfiles applies current auth-user bandwidth config to active SSH connections.
func ReconcileBandwidthProfiles(state *State) int {
	if state == nil || state.SSHConnections == nil {
		return 0
	}

	updated := 0
	state.SSHConnections.Range(func(remoteAddr string, sshConn *SSHConnection) bool {
		if sshConn == nil || sshConn.SSHConn == nil {
			return true
		}

		user := sshConn.SSHConn.User()
		oldProfile := sshConn.GetBandwidthProfile()
		nextProfile := buildBandwidthProfileForUser(user)
		if !sshConn.SetBandwidthProfile(nextProfile) {
			return true
		}

		updated++
		version, updatedAt := sshConn.GetBandwidthProfileMeta()
		log.Printf("bandwidth hot-reload applied user=%s remote=%s version=%d updatedAt=%s old={%s} new={%s}",
			user,
			remoteAddr,
			version,
			updatedAt.Format(viper.GetString("time-format")),
			formatBandwidthProfileForLog(oldProfile),
			formatBandwidthProfileForLog(sshConn.GetBandwidthProfile()),
		)

		return true
	})

	return updated
}

// StartBandwidthHotReload starts periodic reconcile for active SSH connection bandwidth profiles.
func StartBandwidthHotReload(state *State) {
	if state == nil || !viper.GetBool("bandwidth-hot-reload-enabled") {
		return
	}

	interval := viper.GetDuration("bandwidth-hot-reload-time")
	if interval <= 0 {
		interval = 20 * time.Second
	}

	log.Printf("bandwidth hot-reload enabled interval=%s", interval)
	ReconcileBandwidthProfiles(state)

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			ReconcileBandwidthProfiles(state)
		}
	}()
}
