// POST deletion keeps the original action-button appearance and CSRF protection.
document.addEventListener('DOMContentLoaded', () => {
  document.querySelectorAll('[data-delete]').forEach(button => {
    button.addEventListener('click', () => {
      const row = button.closest('tr');
      const name = row ? row.innerText.trim().replace(/\s+/g, ' ').slice(0, 160) : 'este registro';
      let consequence = '';
      if (button.dataset.delete.startsWith('/tenants/')) consequence = ' Os imóveis vinculados ficarão vagos.';
      if (button.dataset.delete.startsWith('/properties/')) consequence = ' Todo o histórico deste imóvel será apagado.';
      if (!window.confirm('Excluir ' + name + '?' + consequence + ' Esta ação não pode ser desfeita.')) return;
      const form = document.getElementById('delete-form');
      form.action = button.dataset.delete;
      form.submit();
    });
  });
});
