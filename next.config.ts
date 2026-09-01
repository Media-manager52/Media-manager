import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  images: {
    remotePatterns: [
      { protocol: "https", hostname: "image.tmdb.org", pathname: "/t/p/**" },
      { protocol: "https", hostname: "artworks.thetvdb.com", pathname: "/**" },
      { protocol: "https", hostname: "**.thetvdb.com", pathname: "/**" },
      { protocol: "https", hostname: "us.simkl.in", pathname: "/img_favicon/**" },
    ],
  },
};

export default nextConfig;
