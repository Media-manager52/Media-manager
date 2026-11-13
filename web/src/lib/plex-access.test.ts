import { describe, expect, it } from "vitest"

import { plexAccessCanHelp, plexAccessChangeText, plexAccessErrorText } from "@/lib/plex-access"

describe("Ret Plex", () => {
  it("words every change", () => {
    expect(plexAccessChangeText({ kind: "add_folder", purpose: "altmount", path: "/mnt/altmount" })).toBe("Plex får AltMounts filer i /mnt/altmount, og følger med, når AltMount genstarter.")
    expect(plexAccessChangeText({ kind: "add_folder", purpose: "movies", path: "/libraries/movies" })).toContain("kun læse")
    expect(plexAccessChangeText({ kind: "set_propagation", purpose: "altmount", path: "/data/usenet/altmount" })).toContain("følger nu med")
    expect(plexAccessChangeText({ kind: "add_library_path", purpose: "movies", path: "/libraries/movies", library: "Film" })).toBe("Plex-biblioteket \"Film\" får mappen /libraries/movies.")
    expect(plexAccessChangeText({ kind: "create_library", purpose: "tv", path: "/libraries/tv", library: "Serier" })).toContain("oprettes")
  })

  it("is offered only for what it can fix", () => {
    expect(plexAccessCanHelp(undefined)).toBe(false)
    expect(plexAccessCanHelp({ applied: false, steps: [{ id: "movie_root", state: "blocked", reason: "no_plex" }] })).toBe(false)
    expect(plexAccessCanHelp({ applied: false, steps: [{ id: "movie_root", state: "create" }, { id: "series_root", state: "blocked", reason: "source_not_visible" }] })).toBe(true)
  })

  it("explains errors", () => {
    expect(plexAccessErrorText("cosmos_unauthorized", "x")).toContain("Read Only")
    expect(plexAccessErrorText("other", "the message")).toBe("the message")
  })
})
