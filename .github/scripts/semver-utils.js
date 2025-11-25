// @ts-check

/**
 * Parse a version tag into an array of version components.
 * Useful for version comparison and manipulation.
 *
 * @param {string} tag - Version tag (e.g., "cli/v0.1.2" or "cli/v0.1.2-rc.3")
 * @returns {number[]} Array of [major, minor, patch]
 */
export function parseVersionArray(tag) {
    if (!tag) return [];
    const version = tag.replace(/^.*v/, "").replace(/-rc\.\d+$/, "");
    return version.split(".").map(Number);
}

/**
 * Escape special regex characters in a string.
 *
 * @param {string} str - String to escape
 * @returns {string} Escaped string safe for use in regex
 */
export function escapeRegex(str) {
    return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
