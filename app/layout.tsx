import type { Metadata, Viewport } from "next";
// Fonts are bundled with the UI; the owner console never loads a font CDN.
import "@fontsource-variable/inter";
import "@fontsource-variable/geist-mono";
import "./globals.css";
import { themeBootScript } from "@/lib/theme";

export const metadata: Metadata = {
  title: {
    default: "Media Manager",
    template: "%s · Media Manager",
  },
  description: "A self-hosted movie and TV media manager with evidence-backed release decisions, first-class season packs, AltMount, Seerr, and Plex.",
  icons: {
    icon: "/favicon.svg",
    shortcut: "/favicon.svg",
  },
};

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: dark)", color: "#0c0d10" },
    { media: "(prefers-color-scheme: light)", color: "#f5f6f8" },
  ],
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    // The stored theme is applied before paint; the class may differ from the
    // server default, so hydration must not warn about it.
    <html lang="en" className="dark" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: themeBootScript }} />
      </head>
      <body className="antialiased">{children}</body>
    </html>
  );
}
