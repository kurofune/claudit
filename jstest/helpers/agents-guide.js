// agentsGuide reads the Agents view's reading guide: the #agents-guide
// template's markup in web/index.html.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { WEB } from './module-graph.js';

export function agentsGuide(html = readFileSync(join(WEB, 'index.html'), 'utf8')) {
  const m = /<template id="agents-guide">([\s\S]*?)<\/template>/.exec(html);
  return m ? m[1] : '';
}
