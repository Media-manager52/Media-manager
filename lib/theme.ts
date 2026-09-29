/**
 * Owner theme preference. Dark is the default; "system" follows the operating
 * system. The preference lives only in this browser's localStorage and is
 * applied before first paint by themeBootScript, so there is no flash.
 */
export type ThemePreference = "dark" | "light" | "system"

export const themeStorageKey = "mm-theme"
export const themePreferences: ThemePreference[] = ["dark", "light", "system"]

export function parseThemePreference(value: string | null | undefined): ThemePreference {
  return value === "light" || value === "system" || value === "dark" ? value : "dark"
}

export function resolveDarkTheme(preference: ThemePreference, systemPrefersDark: boolean): boolean {
  if (preference === "system") return systemPrefersDark
  return preference === "dark"
}

/** Inline, dependency-free script that applies the stored theme pre-paint. */
export const themeBootScript = `(function(){try{var p=localStorage.getItem(${JSON.stringify(themeStorageKey)});var d=p==="light"?false:p==="system"?window.matchMedia("(prefers-color-scheme: dark)").matches:true;document.documentElement.classList.toggle("dark",d)}catch(e){}})()`
