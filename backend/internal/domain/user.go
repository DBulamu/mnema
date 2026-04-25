package domain

import "time"

// User is a registered Mnema account. Created lazily on first successful
// magic-link consume — there is no separate signup step.
type User struct {
	ID               string
	Email            string
	DisplayName      *string
	AvatarURL        *string
	Timezone         string
	DailyPushTime    *string
	DailyPushEnabled bool
	CreatedAt        time.Time
}
