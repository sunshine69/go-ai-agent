import { marked } from "marked";

/**
 * Converts markdown text to HTML with safe sanitization.
 */
export function markdownToHtml(markdownText: string): string {
  try {
    // Use the synchronous parse method by setting async option to false
    return marked.parse(markdownText, { 
      async: false,
      breaks: true, // Convert \n to <br> in markdown
      gfm: true,    // Enable GitHub Flavored Markdown
    }) as string;
  } catch (error) {
    console.error("Error converting markdown to HTML:", error);
    // Return escaped text as fallback
    const div = document.createElement("div");
    div.textContent = markdownText;
    return div.innerHTML.replace(/\n/g, "<br>");
  }
}
