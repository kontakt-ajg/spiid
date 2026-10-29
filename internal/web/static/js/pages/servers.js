import {call} from '../api.js';
import {el, button, clear, notice} from '../dom.js';
import {icon} from '../icons.js';
import {serverForm} from './server_form.js';

export function serversPage(openBrowse) {
  const node = el('section', {class:'page'}), frame = el('div', {class:'frame'});
  node.append(frame);

  async function edit(existing) {
    const result = await serverForm(existing);
    if (!result) return;
    try {
      if (result.delete) await call('servers.remove', {name:existing.name});
      else if (existing) await call('servers.update', {
        old_name:existing.name, server:result.server, secret:result.secret
      });
      else await call('servers.add', {server:result.server, secret:result.secret || ''});
    } catch (e) {
      notice(e.message, true);
    }
    await load();
  }

  async function load() {
    try {
      const {servers} = await call('servers.list');
      clear(frame);
      const add = () => button('Add server', () => edit(), 'primary');
      const header = el('div', {class:'row'}, [el('h2', {class:'grow', text:'Servers'})]);
      if (servers.length) header.append(add());
      frame.append(header);
      if (!servers.length) frame.append(el('div', {class:'empty stack'}, [
        icon('computer'), el('p', {text:'No servers yet'}), add()
      ]));
      for (const s of servers) {
        const card = el('div', {
          class:'card row server-card', attrs:{tabindex:0, role:'button', 'aria-label':s.name},
          on:{
            click:() => openBrowse(s.name), keydown:e => {
              if (e.target === card && (e.key === 'Enter' || e.key === ' ')) {
                e.preventDefault();
                openBrowse(s.name);
              }
            }
          }
        });
        const info = button('', e => {
          e.stopPropagation();
          edit(s);
        }, 'text-button');
        info.setAttribute('aria-label','Edit');
        info.title = 'Edit';
        info.append(icon('info'));
        card.append(icon('computer'), el('div', {class:'grow'}, [
          el('strong', {text:s.name, class:'ellipsis'}),
          el('div', {class:'row wrap'}, ['SFTP', `${s.user}@${s.host}:${s.port}`,
            {key:'Key', password:'Password', agent:'Agent'}
            [s.auth] || 'Agent'
          ].map(text => el('span', {class:'badge', text})))
        ]), info);
        frame.append(card);
      }
    } catch (e) {
      notice(e.message, true);
    }
  }
  return {node, onShow:load};
}
