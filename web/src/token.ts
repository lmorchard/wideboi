// Keep a link credential only in the current page's memory. The fragment is
// preferred for new links; query tokens are accepted for older links.
export function consumeLinkToken(location: Location, history: History): string {
  const url = new URL(location.href);
  const fragment = new URLSearchParams(url.hash.slice(1));
  const token = fragment.get('token') ?? url.searchParams.get('token') ?? '';
  let changed = false;

  if (url.searchParams.has('token')) {
    url.searchParams.delete('token');
    changed = true;
  }
  if (fragment.has('token')) {
    fragment.delete('token');
    url.hash = fragment.toString();
    changed = true;
  }
  if (changed) {
    history.replaceState(history.state, '', url.href);
  }
  return token;
}
