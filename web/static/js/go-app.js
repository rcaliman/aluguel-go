// POST deletion keeps the original action-button appearance and CSRF protection.
document.addEventListener('DOMContentLoaded', () => {
  document.querySelectorAll('[data-delete]').forEach(button => {
    button.addEventListener('click', () => {
      if (!window.confirm('Tem certeza de que deseja excluir este registro?')) return;
      const form = document.getElementById('delete-form');
      form.action = button.dataset.delete;
      form.submit();
    });
  });
});
