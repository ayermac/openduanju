// Command hongguo-probe performs one bounded real probe against the source:
// it builds the anonymous device profile if needed, reads one catalogue page
// and resolves one episode. The device identity is never rotated and no
// credential is printed.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/beilunyang/openhongguo/adapters"
	"github.com/beilunyang/openhongguo/sdk"
)

func main() {
	profile := os.Getenv("HONGGUO_PROFILE_PATH")
	if profile == "" {
		profile = "./var/hongguo/anonymous.json"
	}
	zero := 0
	client, err := adapters.NewClient(profile, sdk.Options{
		// No device rotation: the identity is stable and owned by the
		// operator. The probe never works around a limit.
		DeviceResets:  sdk.Int(zero),
		RetryAttempts: 1,
	})
	if err != nil {
		fail("client", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	page, err := client.Discover(ctx, sdk.DiscoverOptions{})
	if err != nil {
		fail("discover", err)
	}
	fmt.Printf("discover: items=%d\n", len(page.Items))
	if len(page.Items) == 0 {
		fmt.Println("discover: no items")
		return
	}
	series := page.Items[0]
	fmt.Printf("series: id=%s title=%q\n", series.ID, series.Title)

	episodes, err := client.ListEpisodes(ctx, series.ID, sdk.EpisodeOptions{})
	if err != nil {
		fail("episodes", err)
	}
	fmt.Printf("episodes: count=%d\n", len(episodes.Items))
	if len(episodes.Items) == 0 {
		fmt.Println("episodes: none")
		return
	}
	episode := episodes.Items[0]
	fmt.Printf("episode: id=%s number=%d title=%q\n", episode.ID, episode.Number, episode.Title)

	playback, err := client.ResolvePlayback(ctx, episode.ID, sdk.PlaybackOptions{})
	if err != nil {
		fail("resolve", err)
	}
	// The probe reports the shape of the answer, never a stream URL or key.
	fmt.Printf("resolve: ok episode=%s media=%s protected=%t streams=%d expires_in=%ds\n",
		playback.EpisodeID, playback.MediaType, playback.Protected, len(playback.Streams),
		playback.ExpiresAt-time.Now().Unix())
}

// fail prints a classified failure. The message never contains a token.
func fail(stage string, err error) {
	class := "unknown"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		class = "timeout"
	case isNetwork(err):
		class = "network"
	default:
		class = "api"
	}
	fmt.Printf("%s: failed (%s): %v\n", stage, class, err)
	os.Exit(1)
}

// isNetwork reports whether the failure is a transport problem.
func isNetwork(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr)
}
