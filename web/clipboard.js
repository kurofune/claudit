// @ts-check
// copyText puts text on the clipboard: the Clipboard API first, then, when
// it is missing or refuses, a selected off-screen textarea and
// execCommand('copy'). Resolves whether either worked.

/** @param {string} text @returns {Promise<boolean>} */
export async function copyText(text) {
  try {
    if (typeof navigator !== 'undefined' && navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch { /* fall through to the textarea */ }
  return textareaCopy(text);
}

/** @param {string} text */
function textareaCopy(text) {
  const ta = document.createElement('textarea');
  ta.setAttribute('readonly', '');
  ta.setAttribute('aria-hidden', 'true');
  ta.style.position = 'fixed';
  ta.style.top = '-1000px';
  ta.style.opacity = '0';
  ta.value = text;
  ta.textContent = text;
  document.body.appendChild(ta);
  let ok = false;
  try {
    if (typeof ta.select === 'function') ta.select();
    ok = document.execCommand('copy') === true;
  } catch { ok = false; }
  ta.remove();
  return ok;
}
