import { describe, expect, it } from "vitest"

import { calendarBadge } from "@/pages/calendar"

describe("the calendar badge", () => {
  it("says an episode is coming when its day is ahead and it is not in Plex", () => {
    expect(calendarBadge({ workflow_state: "waiting" }, true).text).toBe("Kommer")
  })

  it("never hides progress, a problem or that an episode is already in Plex", () => {
    expect(calendarBadge({ workflow_state: "plex_confirmed" }, true).text).toBe("I Plex")
    expect(calendarBadge({ workflow_state: "downloading" }, true).text).toBe("Henter")
    expect(calendarBadge({ workflow_state: "needs_attention" }, true).text).toBe("Kræver handling")
  })

  it("shows the real state for today and earlier days", () => {
    expect(calendarBadge({ workflow_state: "waiting" }, false).text).toBe("Venter")
  })
})
