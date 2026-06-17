/** The base CDN domain new zones live under. Configurable (not hardcoded) — set
    VITE_CDN_BASE_DOMAIN to your own (e.g. "cdn.example.com"); defaults to the live
    Brisk domain. A new zone named "Test Site" suggests `test-site.<base>`, which is
    the hostname customers CNAME their own domain to (or add a custom domain). */
export const CDN_BASE_DOMAIN = (
  import.meta.env.VITE_CDN_BASE_DOMAIN ?? "cdn.a2zjav.com"
).replace(/^\.+|\.+$/g, "");

/** slugify turns a zone/site name into a DNS-safe label (lowercase, hyphenated). */
export function slugify(name: string): string {
  return name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 50);
}

/** Suggested CDN hostname for a zone name: `<slug>.<base>`. Empty name -> "". */
export function suggestHostname(name: string): string {
  const slug = slugify(name);
  return slug ? `${slug}.${CDN_BASE_DOMAIN}` : "";
}
