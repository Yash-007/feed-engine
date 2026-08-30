package scraper

// extractScript is evaluated in page context and returns every currently
// mounted post. Selectors are injected from internal/selectors so this file
// holds no hardcoded X markup.
//
// It runs on the whole document each pass; the caller dedupes by post id, so
// re-reading posts still on screen is cheap and harmless.
const extractScript = `
(sel) => {
  const out = [];
  const arts = document.querySelectorAll(sel.tweet);
  for (const art of arts) {
    const rec = { miss: [] };

    // permalink + id: the anchor that wraps the timestamp is the canonical one
    let href = "";
    const timeEl = art.querySelector(sel.time);
    if (timeEl) {
      rec.timestamp = timeEl.getAttribute("datetime") || "";
      const anc = timeEl.closest("a[href]");
      if (anc) href = anc.getAttribute("href") || "";
    } else {
      rec.miss.push("time");
      rec.timestamp = "";
    }
    if (!href) {
      const anc = art.querySelector(sel.permalink);
      if (anc) href = anc.getAttribute("href") || "";
    }
    const m = href.match(/^\/([^\/]+)\/status\/(\d+)/);
    if (!m) {
      // No id means no dedupe key, so the post is unusable. Emit it anyway with
      // an empty id: the caller skips those but still counts the article, which
      // is what makes a broken permalink selector show up in the health line
      // instead of looking like an empty timeline.
      rec.miss.push("permalink");
      rec.id = ""; rec.url = ""; rec.author = ""; rec.author_name = "";
      rec.text = ""; rec.has_media = false; rec.is_repost = false;
      rec.is_promoted = false; rec.visible = false;
      out.push(rec);
      continue;
    }
    rec.author = m[1];
    rec.id = m[2];
    rec.url = "https://x.com" + href.split("/analytics")[0].split("/photo")[0];

    // display name: first line of the User-Name block
    const un = art.querySelector(sel.username);
    if (un) {
      rec.author_name = (un.innerText || "").split("\n")[0].trim();
    } else {
      rec.miss.push("username");
      rec.author_name = "";
    }

    // body text; genuinely absent on image-only posts, so track it but keep going
    const tx = art.querySelector(sel.text);
    rec.text = tx ? (tx.innerText || "").trim() : "";
    if (!tx) rec.miss.push("text");

    rec.has_media = !!(art.querySelector(sel.photo) || art.querySelector(sel.video) ||
                       art.querySelector(sel.card) || art.querySelector(sel.poll));
    rec.is_repost = !!art.querySelector(sel.social);
    rec.is_promoted = !!art.querySelector(sel.promoted);

    // on-screen box, used to decide whether an element screenshot is worth taking
    const r = art.getBoundingClientRect();
    rec.visible = r.height > 0 && r.top < window.innerHeight && r.bottom > 0;

    out.push(rec);
  }
  return out;
}
`
