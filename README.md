# Aluguel

Aplicação de gestão de imóveis e aluguéis escrita em Go, com MySQL e interface web incorporada ao executável.

Inclui cadastros de imóveis, inquilinos, locadores, contatos e usuários; histórico dos imóveis; rateio de energia residencial e comercial; recibos em duas vias; contratos editáveis e consulta de correção pelo IPCA.

## Requisitos

- Go 1.26.11 ou superior para compilar.
- Acesso a um MySQL com o esquema da aplicação.
- Docker com Compose apenas para executar a suíte de integração em banco descartável.

O executável contém os templates HTML, CSS, fontes dos ícones e scripts da interface. Não precisa de Go instalado no servidor nem de uma etapa separada de build do frontend.

## Configuração e execução

Crie uma configuração local a partir do exemplo:

```sh
cp config.example.json config.json
chmod 600 config.json
```

Edite `config.json` com o endereço e as credenciais do banco. Para uma instalação já configurada, continue utilizando seu arquivo atual.

Compile e verifique a conexão e as colunas esperadas:

```sh
make build
./bin/aluguel -config config.json -check
```

Inicie o servidor:

```sh
./bin/aluguel -config config.json
```

A configuração de exemplo usa a porta `8080`. O caminho de `-config` pode ser absoluto, permitindo executar o programa a partir de qualquer diretório. Para desenvolvimento:

```sh
make run
```

### Principais opções

| Opção | Uso |
|---|---|
| `address` | Endereço HTTP, por exemplo `:8080`. |
| `database.host`, `database.port`, `database.name` | Servidor, porta e nome do MySQL. |
| `database.username`, `database.password` | Credenciais de acesso ao banco. |
| `database.timezone` | Fuso de apresentação das datas, por exemplo `America/Sao_Paulo`. |
| `database.parameters` | Opções da conexão, incluindo `tls`, `charset` e `collation`. |
| `database.max_open_connections`, `database.max_idle_connections` | Limites do pool de conexões. |
| `session_timeout_seconds` | Tempo máximo de inatividade da sessão; padrão de 1.800 segundos. |
| `secure_cookies` | Use `true` quando o acesso ocorrer por HTTPS. |

O arquivo é lido na inicialização; alterações exigem reiniciar o processo. As sessões ficam na memória, portanto uma reinicialização exige novo login.

Se o MySQL estiver em outro computador, ajuste `database.host`: `localhost` aponta para a máquina que executa a aplicação.

## Banco e regras de negócio

A aplicação usa as tabelas `tb_tenants`, `tb_landlords`, `tb_contacts`, `tb_properties`, `tb_properties_logs`, `tb_users`, `tb_energy`, `tb_commercial_energy` e `tb_energy_title`.

A inicialização verifica o esquema e não cria tabelas, altera colunas ou modifica dados automaticamente. IDs são atribuídos pelo `AUTO_INCREMENT`. Os usuários existentes continuam autenticando com seus hashes BCrypt; novas senhas usam custo 10. As permissões são `ADMIN`, `OPERADOR` e `LEITOR`.

- Cadastros, contatos, exclusões e histórico são gravados em transações.
- Excluir um inquilino libera os imóveis vinculados e registra a desocupação no histórico.
- Somente a última leitura de cada tabela de energia pode ser alterada. A regra é validada na interface e no servidor, inclusive para formulários desatualizados. O histórico completo pode ser consultado em `/energies?history=1`.
- Consumo residencial zero remove rateios anteriores. Uma conta zerada gera parcelas zero; uma conta positiva sem consumo suficiente para dividi-la fica com rateio indisponível, exibido como “—”.
- O formulário `/energies/titleform` carrega os cinco títulos atuais. Alterar apenas um preserva os demais; cada salvamento mantém um novo registro dos títulos.
- A referência do IPCA considera mudanças no valor do aluguel ou na locação. Alterar apenas endereço, observação ou dia de pagamento não reinicia essa referência.
- Os recibos de condomínio usam a data do mês seguinte, incluindo a virada do ano.
- Datas de auditoria são armazenadas e lidas em UTC. Campos `DATE` conservam a data civil, e a interface apresenta os horários no fuso configurado.

A consulta ao Bacen tem tempo limite; uma falha não impede abrir o imóvel. O editor de contratos permite alterar o texto no navegador e imprimir.

## Raspberry Pi 4 com Debian

Compile as variantes de 64 e 32 bits:

```sh
make build-rpi
```

No Raspberry, identifique a arquitetura com `dpkg --print-architecture`:

| Arquitetura do Debian | Executável |
|---|---|
| `arm64` | `bin/aluguel-linux-arm64` |
| `armhf` | `bin/aluguel-linux-armv7` |

Copie o executável correspondente e seu `config.json` para o dispositivo. Os binários são estáticos e não dependem de bibliotecas C no destino.

Exemplo para 64 bits:

```sh
chmod +x aluguel-linux-arm64
chmod 600 config.json
./aluguel-linux-arm64 -config config.json -check
./aluguel-linux-arm64 -config config.json
```

Para atualizar, pare o processo atual, substitua o executável e inicie novamente. Recarregue o navegador com `Ctrl+Shift+R` para atualizar os recursos da interface.

## Organização

```text
cmd/aluguel/                 Entrada do servidor
internal/app/               Configuração, HTTP, persistência e regras de negócio
internal/app/*_test.go       Testes unitários e de integração
internal/app/testdata/       Esquema exclusivo dos testes MySQL
web/templates/              Templates HTML nativos Go
web/static/                 CSS, scripts, ícones e bibliotecas da interface
scripts/test-mysql.sh        Execução do MySQL descartável para testes
compose.test.yml            Ambiente de integração
config.example.json         Exemplo de configuração sem credenciais reais
Makefile                    Comandos de build, execução e testes
```

As páginas mantêm os componentes, cores e navegação da aplicação. Edite os arquivos em `web/templates/` e `web/static/` diretamente; recompilar incorpora as alterações ao executável. As licenças e avisos das bibliotecas da interface ficam em `web/static/vendor/`.

## Testes

```sh
make test
go vet ./...
```

O alvo `test` executa os testes Go com detector de concorrência. Inclui autenticação, permissões, CSRF, transações, datas, rateios, proteção do histórico, títulos, documentos, renderização e escape de HTML. Os testes unitários de persistência usam SQL mock e não acessam o banco da aplicação.

Para testar as gravações em um MySQL 8.4 descartável:

```sh
make integration
```

A suíte usa a porta local `13306`, o banco `aluguel_test`, credenciais próprias e armazenamento temporário. Não lê `config.json` e remove o contêiner ao terminar. O SQL em `internal/app/testdata/mysql.sql` é exclusivo dos testes e não deve ser aplicado a um banco com dados.

Também é possível usar um banco de testes vazio, cujo nome termine em `_test`:

```sh
ALUGUEL_TEST_DSN='usuario:senha@tcp(localhost:3306)/aluguel_test' \
  go test -tags=integration -count=1 ./internal/app
```

A suíte recusa bancos que já contenham tabelas e remove as tabelas que criou ao terminar.

## Arquivos locais

`config.json`, configurações privadas, logs, diretórios de ferramentas e executáveis gerados ficam fora do versionamento pelo `.gitignore`. Publique o código-fonte e `config.example.json`; mantenha as credenciais reais apenas no ambiente de execução.
# aluguel-go

## Atualização de produção com as correções de segurança

Esta atualização não executa migrações e não exige novas tabelas ou colunas.
Mantém os tipos numéricos, os documentos e os hashes de senha existentes. O
`config.json` atual continua válido. A aplicação verifica que suas tabelas usam
InnoDB, necessário para as transações e os bloqueios. Se encontrar outro motor,
recusa iniciar com uma mensagem explicativa, sem converter nada. Nesse caso,
conserve o binário anterior e planeje a conversão em uma cópia do banco primeiro.

A coluna `tb_tenants.email`, incluída no formulário, é opcional: quando ausente,
o formulário usa os contatos para registrar e-mails e os cadastros continuam
funcionando. A verificação `-check` informa essa condição. Não aplique a fixture
`internal/app/testdata/mysql.sql` em produção.

### Preparação sem substituir o executável em uso

Na máquina de desenvolvimento, com Go 1.26.11+ e Node.js 18+ para os testes de interface:

```sh
make verify
make integration
make candidate
```

`make integration` precisa de Docker com Compose e usa somente um MySQL
8.4 descartável. Os cenários adicionais incluem inserções concorrentes na
primeira leitura de energia, conflito entre edições, tentativas simultâneas de
remover a última permissão de administrador e funcionamento sem a coluna de
e-mail. A CI em `.github/workflows/test.yml` também executa essa integração.
O banco real não é acessado por esses comandos.

Se o servidor tiver arquitetura diferente, use `make build-rpi` e selecione o
binário correspondente. Compile e verifique antes de parar o serviço atual.

### Procedimento de implantação

1. Faça um backup restaurável do banco, guarde uma cópia do executável atual e
   preserve o `config.json`. Use sua ferramenta habitual de backup. Teste a
   restauração em um banco separado antes de depender desse backup.
2. Copie o novo executável ao servidor com um nome separado, por exemplo
   `aluguel-candidate`, no mesmo diretório do executável atual. Não sobrescreva
   um arquivo em execução. Ajuste a permissão com `chmod +x aluguel-candidate`.
3. Nesse diretório, execute a verificação somente de leitura:

   ```sh
   ./aluguel-candidate -config /caminho/real/config.json -check
   ```

   Substitua o caminho pelo arquivo já usado em produção. Só prossiga se a
   verificação concluir com sucesso. Nenhum DDL é executado por esse comando.
4. Em uma janela curta de manutenção, encerre a instância antiga com SIGTERM
   ou pelo gerenciador de serviço já utilizado. Aguarde seu encerramento, então
   renomeie o executável antigo para `aluguel-previous` e o candidato para o
   nome utilizado pelo serviço. Inicie pelo mesmo gerenciador e com os mesmos
   argumentos. Não mantenha versões diferentes escrevendo simultaneamente.
5. Faça novo login e recarregue o navegador com `Ctrl+Shift+R`. As sessões são
   em memória e o reinício exige autenticação. Reabra os formulários de edição
   que estavam abertos antes da atualização.
6. Confira login de cada perfil utilizado, listagens e buscas, abertura de um
   cadastro, geração/impressão de recibo e contrato e consulta de energia.
   Valide a gravação primeiro em homologação com cópia do banco. Observe os
   logs do serviço para erros de conexão, esquema ou transação.

Os nomes de serviço e diretórios dependem da instalação; não há um serviço
systemd ou implantação Docker de produção definido neste repositório.

### Reversão

Pare a instância nova, devolva `aluguel-previous` ao nome original e inicie pelo
mesmo gerenciador. Esta versão não altera o esquema nem converte dados, portanto
não precisa de uma migração inversa. Gravações válidas feitas depois da atualização
continuam no banco e senhas novas continuam usando BCrypt compatível. Restaurar
um backup do banco é uma operação separada que descartaria gravações posteriores;
não é necessário fazê-lo para apenas reverter o executável.

### Mudanças percebidas pelos usuários

- Trocar a senha exige a senha atual. Novas senhas precisam de pelo menos oito
  caracteres e no máximo 72 bytes; senhas antigas continuam autenticando.
- Alterações administrativas em usuários encerram suas sessões. Senhas
  redefinidas invalidam as sessões anteriores, inclusive em outras instâncias
  desta versão ao receberem a próxima requisição.
- O login permite até dez tentativas por conta e sessenta por origem em um
  minuto, com até quatro verificações simultâneas por processo. Excesso retorna
  HTTP 429 com `Retry-After`. Atrás de um proxy, o limite por origem é compartilhado:
  cabeçalhos encaminhados não são aceitos como endereço confiável.
- Cadastros e histórico de energia mostram até cinquenta registros por página.
  A seleção de imóveis para recibos pertence à página atual e não é mantida ao
  trocar de página. Uma emissão aceita no máximo quinhentos IDs distintos ou
  repetidos no pedido. A visão recente de energia continua mostrando três
  leituras residenciais e duas comerciais.
- Formulários antigos ou modificados por outra pessoa precisam ser reabertos;
  conflitos são informados sem sobrescrever silenciosamente os dados. A proteção
  compara o registro completo e seus contatos, sem depender da precisão de
  `updated_at`. Todos os escritores devem usar esta versão para respeitá-la.
- Novas gravações validam vencimento entre 1 e 31, aluguel não negativo, tamanho
  dos textos, formato de e-mail e CPF/CNPJ com 11 ou 14 dígitos. A validação de
  documentos não verifica dígitos verificadores. Documentos formatados são
  preservados, mas duplicatas equivalentes são recusadas. Dados antigos não
  são corrigidos automaticamente: um registro inválido pode exigir correção ao
  ser editado. Dias como 31 continuam sendo rejeitados ao gerar um contrato em
  um mês sem esse dia, em vez de mover a data silenciosamente.
- O IPCA usa cache por período por seis horas e cache de falhas por trinta
  segundos. A primeira consulta pode aguardar até três segundos; falhas, HTTP
  404 e respostas sem índices são mostradas como indisponíveis, nunca como 0%.
- A confirmação de exclusão informa o registro e as consequências de liberar
  imóveis ou apagar histórico.

### Campo de e-mail separado: habilitação opcional

Não é necessário executar SQL para implantar esta atualização. Caso queira o
campo de e-mail separado e ele ainda não exista, faça backup, teste a operação
em homologação e confirme a versão do MySQL. No cliente SQL conectado ao banco
correto, consulte primeiro:

```sql
SELECT DATABASE(), VERSION();
SHOW COLUMNS FROM tb_tenants LIKE 'email';
```

Somente se a segunda consulta não retornar nenhuma linha, execute:

```sql
ALTER TABLE tb_tenants ADD COLUMN email VARCHAR(255) NULL DEFAULT NULL;
```

A adição é nullable e não reescreve valores da aplicação nem invalida o binário
anterior. O tempo de execução e possíveis bloqueios do DDL dependem da versão
e do tamanho da tabela: não presuma que seja uma operação instantânea. Reinicie
a aplicação depois para reconhecer a coluna. Não remova a coluna ao reverter o
executável, pois isso apagaria os e-mails já registrados.

### Limites desta atualização

Não há conversão de `DOUBLE` para `DECIMAL` nem alteração da política de
arredondamento dos rateios. Essa mudança precisa de comparação dos resultados
com dados reais, reconciliação dos valores e migração própria. Não há criação
automática de índices: avalie os índices existentes e `EXPLAIN` antes de alterar
o banco. A verificação `-check` valida colunas e motor, não certifica todos os
tipos, índices ou a qualidade dos dados existentes.

Os testes JavaScript verificam as confirmações de exclusão com DOM simulado;
os testes Go verificam renderização e fluxos HTTP. Eles não substituem a
conferência em navegador de máscaras, impressão, dispositivos móveis e teclado.
