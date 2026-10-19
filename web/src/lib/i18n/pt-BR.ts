import type { Dictionary } from "./en";

/** Brazilian Portuguese strings (primary audience). Same keys as en.ts. */
export const ptBR: Dictionary = {
  "app.name": "partyline",

  "common.loading": "Carregando…",
  "common.retry": "Tentar de novo",
  "common.cancel": "Cancelar",
  "common.save": "Salvar",
  "common.copy": "Copiar",
  "common.copied": "Copiado",
  "common.close": "Fechar",

  "validation.required": "Obrigatório",
  "validation.username": "De 3 a 32 caracteres: letras minúsculas, números, _ ou -",
  "validation.passwordMin": "Pelo menos {min} caracteres",
  "validation.tooLong": "No máximo {max} caracteres",

  "error.network": "Não foi possível falar com o servidor. Verifique sua conexão e tente de novo.",
  "error.generic": "Algo deu errado. Tente de novo.",
  "nav.label": "Principal",
  "nav.overview": "Visão geral",
  "nav.rooms": "Salas",
  "nav.agents": "Agentes",
  "nav.connectors": "Conectores",
  "nav.identities": "Identidades",
  "nav.tokens": "Tokens de API",
  "nav.settings": "Configurações",
  "nav.admin": "Administração",
  "nav.adminUsers": "Usuários",
  "nav.groupWork": "Trabalho",
  "nav.groupConnections": "Conexões",
  "nav.groupAccount": "Conta",
  "nav.toggleSidebar": "Mostrar ou ocultar a barra lateral",

  "shell.userMenu": "Menu da conta",
  "shell.logout": "Sair",
  "shell.roleAdmin": "Admin",

  "theme.label": "Tema",
  "theme.system": "Sistema",
  "theme.light": "Claro",
  "theme.dark": "Escuro",

  "locale.label": "Idioma",

  "live.open": "Ao vivo",
  "live.connecting": "Conectando",
  "live.closed": "Desconectado",
  "live.reconnectingTitle": "Conexão perdida. Reconectando…",
  "live.reconnectingBody":
    "As atualizações ao vivo voltam sozinhas; o que você perdeu é buscado quando a linha voltar.",

  "forbidden.title": "Somente administradores",
  "forbidden.body":
    "Esta área gerencia contas e convites. Peça acesso a um administrador da instância.",
  "forbidden.back": "Voltar para a visão geral",

  "notFound.title": "Nada nesta linha",
  "notFound.body": "A página que você abriu não existe ou foi movida.",
  "notFound.back": "Ir para a visão geral",

  "crash.title": "Esta tela encontrou um erro",
  "crash.body": "Recarregue a página. Se continuar acontecendo, o log do servidor tem os detalhes.",

  "page.overviewTitle": "Visão geral",
  "page.overviewBody": "Quem está na linha e o que está passando pelas suas salas.",
  "page.roomsTitle": "Salas",
  "page.roomsBody":
    "Uma sala liga agentes, chats e pessoas. Uma mensagem de um membro chega aos outros.",
  "page.agentsTitle": "Agentes",
  "page.agentsBody": "Sessões do Claude Code que entraram pelo partyline channel.",
  "page.connectorsTitle": "Conectores",
  "page.connectorsBody": "Suas contas de WhatsApp, Telegram, Discord e webhook.",
  "page.identitiesTitle": "Identidades",
  "page.identitiesBody":
    "Contas de chat verificadas como suas. Seus agentes confiam nas mensagens delas.",
  "page.tokensTitle": "Tokens de API",
  "page.tokensBody": "Tokens para agentes e scripts. O segredo aparece uma única vez, na criação.",
  "page.settingsTitle": "Configurações",
  "page.settingsBody": "Perfil, senha, sessões, tema e idioma.",
  "page.adminUsersTitle": "Usuários",
  "page.adminUsersBody": "Contas desta instância, seus papéis e situação.",
  "page.emptyTitle": "Nada aqui ainda",
};
