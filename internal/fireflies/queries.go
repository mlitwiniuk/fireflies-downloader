package fireflies

import (
	"fmt"
	"strings"
)

const listTranscriptsQuery = `
query Transcripts(
  $limit: Int
  $skip: Int
  $fromDate: DateTime
  $toDate: DateTime
  $userId: String
  $mine: Boolean
  $organizers: [String!]
  $participants: [String!]
  $channelId: String
  $keyword: String
  $scope: String
) {
  transcripts(
    limit: $limit
    skip: $skip
    fromDate: $fromDate
    toDate: $toDate
    user_id: $userId
    mine: $mine
    organizers: $organizers
    participants: $participants
    channel_id: $channelId
    keyword: $keyword
    scope: $scope
  ) {
    id
    title
    date
    dateString
    duration
    organizer_email
    host_email
    participants
    transcript_url
    is_live
  }
}
`

const portableTranscriptFields = `
id
dateString
speakers {
  id
  name
}
sentences {
  index
  speaker_name
  speaker_id
  text
  raw_text
  start_time
  end_time
  ai_filters {
    task
    pricing
    metric
    question
    date_and_time
    text_cleanup
    sentiment
  }
}
title
host_email
organizer_email
calendar_id
user {
  user_id
  email
  name
  num_transcripts
  recent_transcript
  recent_meeting
  minutes_consumed
  is_admin
  integrations
  is_calendar_in_sync
}
fireflies_users
participants
date
transcript_url
duration
meeting_attendees {
  displayName
  email
  phoneNumber
  name
  location
}
meeting_attendance {
  name
  join_time
  leave_time
}
summary {
  keywords
  action_items
  outline
  shorthand_bullet
  overview
  bullet_gist
  gist
  short_summary
  short_overview
  meeting_type
  topics_discussed
  transcript_chapters
  notes
}
cal_id
calendar_type
meeting_info {
  fred_joined
  silent_meeting
  summary_status
}
apps_preview {
  outputs {
    transcript_id
    user_id
    app_id
    created_at
    title
    prompt
    response
  }
}
meeting_link
is_live
channels {
  id
}
`

const completeTranscriptFields = `
id
dateString
privacy
analytics {
  sentiments {
    negative_pct
    neutral_pct
    positive_pct
  }
  categories {
    questions
    date_times
    metrics
    tasks
  }
  speakers {
    speaker_id
    name
    duration
    word_count
    longest_monologue
    monologues_count
    filler_words
    questions
    duration_pct
    words_per_minute
  }
}
speakers {
  id
  name
}
sentences {
  index
  speaker_name
  speaker_id
  text
  raw_text
  start_time
  end_time
  ai_filters {
    task
    pricing
    metric
    question
    date_and_time
    text_cleanup
    sentiment
  }
}
title
host_email
organizer_email
calendar_id
user {
  user_id
  email
  name
  num_transcripts
  recent_transcript
  recent_meeting
  minutes_consumed
  is_admin
  integrations
  is_calendar_in_sync
  user_groups {
    id
    name
    handle
    members {
      user_id
      first_name
      last_name
      email
    }
  }
}
fireflies_users
workspace_users
participants
date
transcript_url
audio_url
video_url
duration
meeting_attendees {
  displayName
  email
  phoneNumber
  name
  location
}
meeting_attendance {
  name
  join_time
  leave_time
}
summary {
  keywords
  action_items
  outline
  shorthand_bullet
  overview
  bullet_gist
  gist
  short_summary
  short_overview
  meeting_type
  topics_discussed
  transcript_chapters
  notes
  extended_sections {
    title
    content
  }
}
cal_id
calendar_type
meeting_info {
  fred_joined
  silent_meeting
  summary_status
}
apps_preview {
  outputs {
    transcript_id
    user_id
    app_id
    created_at
    title
    prompt
    response
  }
}
meeting_link
is_live
channels {
  id
  title
  is_private
  created_by
  created_at
  updated_at
  members {
    user_id
    email
    name
  }
}
shared_with {
  email
  name
  photo_url
  expires_at
}
`

const minimalTranscriptFields = `
id
title
date
dateString
duration
organizer_email
host_email
participants
transcript_url
speakers {
  id
  name
}
sentences {
  index
  speaker_name
  speaker_id
  text
  raw_text
  start_time
  end_time
}
summary {
  keywords
  action_items
  outline
  overview
  gist
  short_summary
  notes
}
meeting_link
is_live
`

const deleteTranscriptMutation = `
mutation DeleteTranscript($id: String!) {
  deleteTranscript(id: $id) {
    id
    title
    host_email
    organizer_email
    fireflies_users
    participants
    date
    transcript_url
    duration
  }
}
`

func transcriptQuery(profile string) (string, error) {
	fields, err := transcriptFields(profile)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`
query Transcript($transcriptId: String!) {
  transcript(id: $transcriptId) {
%s
  }
}
`, indentGraphQL(fields, "    ")), nil
}

func transcriptFields(profile string) (string, error) {
	switch profile {
	case "complete":
		return completeTranscriptFields, nil
	case "portable":
		return portableTranscriptFields, nil
	case "minimal":
		return minimalTranscriptFields, nil
	default:
		return "", fmt.Errorf("unknown profile %q; use complete, portable, or minimal", profile)
	}
}

func fallbackProfiles(profile string) []string {
	switch profile {
	case "complete":
		return []string{"complete", "portable", "minimal"}
	case "portable":
		return []string{"portable", "minimal"}
	case "minimal":
		return []string{"minimal"}
	default:
		return []string{profile}
	}
}

func indentGraphQL(value, prefix string) string {
	lines := strings.Split(strings.Trim(value, "\n"), "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines[index] = prefix + line
	}
	return strings.Join(lines, "\n")
}
