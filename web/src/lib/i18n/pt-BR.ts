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
  "auth.username": "Nome de usuário",
  "auth.usernameHint": "Letras minúsculas, números, _ ou -. De 3 a 32 caracteres.",
  "auth.password": "Senha",
  "auth.passwordHint": "Pelo menos {min} caracteres.",
  "auth.displayName": "Nome de exibição",
  "auth.displayNameHint": "Aparece para as outras pessoas no lugar do nome de usuário.",
  "auth.showPassword": "Mostrar senha",
  "auth.hidePassword": "Ocultar senha",
  "auth.optional": "opcional",

  "login.tape": "De volta à linha",
  "login.title": "Entrar",
  "login.body": "Use a conta que um administrador criou para você ou a que você configurou.",
  "login.submit": "Entrar",
  "login.submitting": "Entrando…",
  "login.invalid": "Usuário ou senha incorretos.",
  "login.rateLimited": "Muitas tentativas. Espere um minuto e tente de novo.",
  "login.noAccount":
    "Ainda não tem conta? Peça um link de convite a um administrador desta instância.",

  "setup.tape": "Primeira inicialização",
  "setup.title": "Configurar o partyline",
  "setup.body":
    "Crie a primeira conta. Ela será a administradora da instância e poderá convidar todo mundo.",
  "setup.token": "Token de configuração",
  "setup.tokenHint":
    "Impresso uma única vez no log do servidor quando a instância inicia sem usuários.",
  "setup.submit": "Criar conta de administrador",
  "setup.submitting": "Criando conta…",
  "setup.badToken": "Esse token de configuração não é válido. Copie de novo do log do servidor.",

  "invite.tape": "Você foi convidado",
  "invite.title": "Entrar neste partyline",
  "invite.body": "Convite como {role}. O link funciona uma vez e expira {expires}.",
  "invite.roleAdmin": "administrador",
  "invite.roleMember": "membro",
  "invite.submit": "Criar conta",
  "invite.submitting": "Criando conta…",
  "invite.expiredTape": "Linha encerrada",
  "invite.expiredTitle": "Este convite não pode ser usado",
  "invite.expiredBody":
    "Ele expirou, foi revogado ou já foi usado. Peça um novo link a quem convidou você.",
  "invite.toLogin": "Ir para o login",
};
