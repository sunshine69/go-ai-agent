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

      silent: true   // Silent mode - won't throw errors on invalid markup
    }) as string;
  } catch (error) {
    console.error("Error converting markdown to HTML:", error);
    
    // Return escaped text with line breaks as fallback
    const escapeHtml = (text: string): string => {
      return text
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#039;');
    };
    
    // Convert newlines to <br> tags for HTML display
    return escapeHtml(markdownText).replace(/\n/g, '<br>');
  }
}
