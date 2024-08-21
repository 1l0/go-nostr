package nip13

import (
	"context"
	"encoding/hex"
	"errors"
	"math/bits"
	"runtime"
	"strconv"
	"time"

	nostr "github.com/nbd-wtf/go-nostr"
)

var (
	ErrDifficultyTooLow = errors.New("nip13: insufficient difficulty")
	ErrGenerateTimeout  = errors.New("nip13: generating proof of work took too long")
	ErrMissingPubKey    = errors.New("nip13: attempting to work on an event without a pubkey, which makes no sense")
)

// CommittedDifficulty returns the Difficulty but checks the "nonce" tag for a target.
//
// if the target is smaller than the actual difficulty then the value of the target is used.
// if the target is bigger than the actual difficulty then it returns 0.
func CommittedDifficulty(event *nostr.Event) int {
	work := 0
	if nonceTag := event.Tags.GetFirst([]string{"nonce", ""}); nonceTag != nil && len(*nonceTag) >= 3 {
		work = Difficulty(event.ID)
		target, _ := strconv.Atoi((*nonceTag)[2])
		if target <= work {
			work = target
		} else {
			work = 0
		}
	}
	return work
}

// Difficulty counts the number of leading zero bits in an event ID.
func Difficulty(id string) int {
	var zeros int
	var b [1]byte
	for i := 0; i < 64; i += 2 {
		if id[i:i+2] == "00" {
			zeros += 8
			continue
		}
		if _, err := hex.Decode(b[:], []byte{id[i], id[i+1]}); err != nil {
			return -1
		}
		zeros += bits.LeadingZeros8(b[0])
		break
	}
	return zeros
}

// Check reports whether the event ID demonstrates a sufficient proof of work difficulty.
// Note that Check performs no validation other than counting leading zero bits
// in an event ID. It is up to the callers to verify the event with other methods,
// such as [nostr.Event.CheckSignature].
func Check(id string, minDifficulty int) error {
	if Difficulty(id) < minDifficulty {
		return ErrDifficultyTooLow
	}
	return nil
}

// Deprecated: use DoWork()
func Generate(event *nostr.Event, targetDifficulty int, timeout time.Duration) (*nostr.Event, error) {
	if event.PubKey == "" {
		return nil, ErrMissingPubKey
	}

	tag := nostr.Tag{"nonce", "", strconv.Itoa(targetDifficulty)}
	event.Tags = append(event.Tags, tag)
	var nonce uint64
	start := time.Now()
	for {
		nonce++
		tag[1] = strconv.FormatUint(nonce, 10)
		if Difficulty(event.GetID()) >= targetDifficulty {
			return event, nil
		}
		// benchmarks show one iteration is approx 3000ns on i7-8565U @ 1.8GHz.
		// so, check every 30ms; arbitrary
		if nonce%10000 == 0 && time.Since(start) > timeout {
			return nil, ErrGenerateTimeout
		}
	}
}

// DoWork() performs work in multiple threads (given by runtime.NumCPU()) and returns the first
// nonce (as a nostr.Tag) that yields the required work.
// Returns an error if the context expires before that.
func DoWork(ctx context.Context, event nostr.Event, targetDifficulty int) (nostr.Tag, error) {
	if event.PubKey == "" {
		return nil, ErrMissingPubKey
	}

	ctx, cancel := context.WithCancel(ctx)
	nthreads := runtime.NumCPU()
	var nonceTag nostr.Tag

	for i := 0; i < nthreads; i++ {
		go func(event nostr.Event, nonce uint64) {
			tag := nostr.Tag{"nonce", "", strconv.Itoa(targetDifficulty)}
			event.Tags = append(event.Tags, tag)
			for {
				// try 10000 times (~30ms)
				for n := 0; n < 10000; n++ {
					tag[1] = strconv.FormatUint(nonce, 10)

					if Difficulty(event.GetID()) >= targetDifficulty {
						nonceTag = tag
						cancel()
						return
					}

					nonce += uint64(nthreads)
				}

				// then check if the context was canceled
				select {
				case <-ctx.Done():
					return
				default:
					// otherwise keep trying
				}
			}
		}(event, uint64(i))
	}

	<-ctx.Done()

	if nonceTag != nil {
		return nonceTag, nil
	}

	return nil, ErrGenerateTimeout
}
